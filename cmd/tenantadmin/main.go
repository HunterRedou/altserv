// Command tenantadmin activates or suspends one tenant without VIES (WP-05).
//
// It is an operator tool, not an HTTP route. Each change writes one audit
// event (tenant.activated or tenant.suspended) in the same transaction. The
// tool never deletes a tenant or its data, and it cannot activate a terminated
// tenant.
//
// Usage:
//
//	go run ./cmd/tenantadmin -tenant <tenant-uuid> -action activate
//	go run ./cmd/tenantadmin -tenant <tenant-uuid> -action suspend
//
// The connection uses DB_URL and must be a member of the altserv_app role,
// because the change runs inside the same tenant-scoped transaction as the
// application.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/tenantadmin"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	tenant := flag.String("tenant", "", "tenant UUID")
	actionFlag := flag.String("action", "", "activate or suspend")
	flag.Parse()

	if *tenant == "" {
		log.Fatal("tenantadmin: -tenant is required")
	}
	tenantID, err := uuid.Parse(*tenant)
	if err != nil {
		log.Fatalf("tenantadmin: -tenant must be a UUID: %v", err)
	}
	action, err := tenantadmin.ParseAction(*actionFlag)
	if err != nil {
		log.Fatalf("tenantadmin: -action: %v", err)
	}

	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		log.Fatal("tenantadmin: DB_URL is required")
	}

	conn, err := sql.Open("pgx", dbURL)
	if err != nil {
		log.Fatalf("tenantadmin: open database: %v", err)
	}
	defer conn.Close()

	if err := conn.Ping(); err != nil {
		log.Fatalf("tenantadmin: ping database: %v", err)
	}

	run := func(ctx context.Context, tenantID uuid.UUID, fn func(*db.Queries) error) error {
		return db.WithTenant(ctx, conn, tenantID, fn)
	}
	result, err := tenantadmin.Apply(context.Background(), run, audit.SQLRecorder{}, tenantID, action, uuid.NewString())
	if err != nil {
		log.Fatalf("tenantadmin: %v", err)
	}

	if !result.Changed {
		fmt.Printf("tenant %s is already %s; nothing changed\n", result.TenantID, result.NewStatus)
		return
	}
	fmt.Printf("tenant %s: %s -> %s\n", result.TenantID, result.OldStatus, result.NewStatus)
}
