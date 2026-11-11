package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/HunterRedou/altserv/internal/activities"
	"github.com/HunterRedou/altserv/internal/assignments"
	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/billing"
	"github.com/HunterRedou/altserv/internal/breakglass"
	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/config"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/observability"
	"github.com/HunterRedou/altserv/internal/patients"
	"github.com/HunterRedou/altserv/internal/reports"
	"github.com/HunterRedou/altserv/internal/server"
	"github.com/HunterRedou/altserv/internal/templates"
	"github.com/HunterRedou/altserv/internal/visits"
	"github.com/joho/godotenv"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type apiConfig struct {
	fileserverHits       atomic.Int32
	db                   *db.Queries
	dbConn               *sql.DB
	platform             string
	sessionDuration      time.Duration
	tenantRunner         tenantRunner
	auditRecorder        audit.Recorder
	patientService       *patients.Service
	assignmentService    *assignments.Service
	catalogService       *catalog.Service
	activityService      *activities.Service
	templateService      *templates.Service
	visitService         *visits.Service
	reportService        *reports.Service
	billingService       *billing.Service
	breakGlassService    *breakglass.Service
	loginLimiter         *loginRateLimiter
	passwordResetLimiter *loginRateLimiter
	mfaBox               *auth.SecretBox
	metrics              *observability.Registry
	health               *observability.Health
	metricsToken         string
}

func main() {
	godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuration: %v", err)
	}

	dbConn, err := sql.Open("pgx", cfg.DBURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer dbConn.Close()

	// Bound the pool so a traffic spike cannot exhaust PostgreSQL connections.
	dbConn.SetMaxOpenConns(cfg.DBMaxOpenConns)
	dbConn.SetMaxIdleConns(cfg.DBMaxIdleConns)
	dbConn.SetConnMaxLifetime(cfg.DBConnMaxLifetime)

	if err := dbConn.Ping(); err != nil {
		log.Fatalf("ping database: %v", err)
	}

	metrics := observability.NewRegistry()
	metrics.SetPoolStats(dbConn.Stats)
	health := observability.NewHealth(dbConn)

	patientCipher, err := patients.NewCipher(cfg.PatientEncryptionKey, cfg.PatientKeyVersion)
	if err != nil {
		log.Fatalf("patient encryption: %v", err)
	}
	searchIndexer, err := patients.NewSearchIndexer(cfg.PatientSearchKey, cfg.PatientSearchKeyVersion)
	if err != nil {
		log.Fatalf("patient search index: %v", err)
	}
	mfaKey := cfg.MFAEncryptionKey
	if len(mfaKey) == 0 {
		mfaKey = auth.DeriveMFAKey(cfg.PatientEncryptionKey)
	}
	mfaBox, err := auth.NewSecretBox(mfaKey, cfg.MFAKeyVersion)
	if err != nil {
		log.Fatalf("MFA encryption: %v", err)
	}

	auditRecorder := audit.SQLRecorder{}
	assignmentService := assignments.NewService(assignments.NewRepository(), auditRecorder)
	patientService := patients.NewServiceWithSearch(patients.NewRepository(patientCipher), auditRecorder, assignmentService, searchIndexer)
	catalogRepository := catalog.NewRepository()
	catalogService := catalog.NewService(catalogRepository, auditRecorder)
	activityService := activities.NewService(activities.NewRepository(patientCipher), catalogRepository, auditRecorder, assignmentService)
	templateService := templates.NewService(templates.NewRepository(), catalogRepository, auditRecorder, assignmentService)
	visitService := visits.NewService(visits.NewRepository(), auditRecorder, assignmentService)
	reportService := reports.NewService(reports.NewRepository(patientCipher), auditRecorder, assignmentService)
	billingService := billing.NewService(billing.NewRepository(), auditRecorder)
	breakGlassService := breakglass.NewService(breakglass.NewRepository(), auditRecorder, patientService)

	apiCfg := apiConfig{
		db:                   db.New(dbConn),
		dbConn:               dbConn,
		platform:             cfg.Platform,
		sessionDuration:      cfg.SessionDuration,
		auditRecorder:        auditRecorder,
		patientService:       patientService,
		assignmentService:    assignmentService,
		catalogService:       catalogService,
		activityService:      activityService,
		templateService:      templateService,
		visitService:         visitService,
		reportService:        reportService,
		billingService:       billingService,
		breakGlassService:    breakGlassService,
		loginLimiter:         newLoginRateLimiter(10, time.Minute),
		passwordResetLimiter: newLoginRateLimiter(5, time.Minute),
		mfaBox:               mfaBox,
		metrics:              metrics,
		health:               health,
		metricsToken:         cfg.MetricsToken,
	}

	// Pick up tenants that were still pending when the previous process stopped.
	apiCfg.resumePendingTenantVerification(context.Background())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	metrics.WatchBackupMarker(ctx, cfg.BackupMarkerFile, time.Minute)
	observability.NewSlowQueryWatcher(dbConn, metrics, cfg.SlowQueryThreshold, 30*time.Second).Run(ctx)
	go apiCfg.runSessionPurge(ctx, cfg.SessionPurgeInterval)

	srv := server.New(cfg.Addr(), apiCfg.routes())
	log.Printf("Serving on %s", cfg.Addr())
	if err := runWithDrain(ctx, health, cfg.ShutdownDrainDelay, srv.Run); err != nil {
		log.Fatalf("server: %v", err)
	}
}
