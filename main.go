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
	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/config"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/patients"
	"github.com/HunterRedou/altserv/internal/server"
	"github.com/HunterRedou/altserv/internal/templates"
	"github.com/HunterRedou/altserv/internal/visits"
	"github.com/joho/godotenv"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type apiConfig struct {
	fileserverHits    atomic.Int32
	db                *db.Queries
	dbConn            *sql.DB
	platform          string
	sessionDuration   time.Duration
	tenantRunner      tenantRunner
	auditRecorder     audit.Recorder
	patientService    *patients.Service
	assignmentService *assignments.Service
	catalogService    *catalog.Service
	activityService   *activities.Service
	templateService   *templates.Service
	visitService      *visits.Service
	loginLimiter      *loginRateLimiter
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

	if err := dbConn.Ping(); err != nil {
		log.Fatalf("ping database: %v", err)
	}

	patientCipher, err := patients.NewCipher(cfg.PatientEncryptionKey, cfg.PatientKeyVersion)
	if err != nil {
		log.Fatalf("patient encryption: %v", err)
	}
	searchIndexer, err := patients.NewSearchIndexer(cfg.PatientSearchKey, cfg.PatientSearchKeyVersion)
	if err != nil {
		log.Fatalf("patient search index: %v", err)
	}
	auditRecorder := audit.SQLRecorder{}
	assignmentService := assignments.NewService(assignments.NewRepository(), auditRecorder)
	patientService := patients.NewServiceWithSearch(patients.NewRepository(patientCipher), auditRecorder, assignmentService, searchIndexer)
	catalogRepository := catalog.NewRepository()
	catalogService := catalog.NewService(catalogRepository, auditRecorder)
	activityService := activities.NewService(activities.NewRepository(patientCipher), catalogRepository, auditRecorder, assignmentService)
	templateService := templates.NewService(templates.NewRepository(), catalogRepository, auditRecorder, assignmentService)
	visitService := visits.NewService(visits.NewRepository(), auditRecorder, assignmentService)

	apiCfg := apiConfig{
		db:                db.New(dbConn),
		dbConn:            dbConn,
		platform:          cfg.Platform,
		sessionDuration:   cfg.SessionDuration,
		auditRecorder:     auditRecorder,
		patientService:    patientService,
		assignmentService: assignmentService,
		catalogService:    catalogService,
		activityService:   activityService,
		templateService:   templateService,
		visitService:      visitService,
		loginLimiter:      newLoginRateLimiter(10, time.Minute),
	}

	// Pick up tenants that were still pending when the previous process stopped.
	apiCfg.resumePendingTenantVerification(context.Background())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(cfg.Addr(), apiCfg.routes())
	log.Printf("Serving on %s", cfg.Addr())
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server: %v", err)
	}
}
