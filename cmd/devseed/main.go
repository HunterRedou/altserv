// Command devseed creates a synthetic development dataset: one demo tenant with
// a chef, an employee and a platform admin, a synthetic catalog with three
// services, a contract, an activity rule, two patients, assignments, a template
// and a planned visit.
//
// It is not a production feature. It refuses to run unless PLATFORM=dev, and it
// refuses to run twice for the same tenant or demo users. The catalog code and
// every service code carry a SYNTH prefix, and the source note says that the
// data is synthetic. Never load a real Brandenburg catalog this way; use
// cmd/catalogimport after professional verification (doc 9).
//
// Usage:
//
//	DEV_SEED_PASSWORD='...' go run ./cmd/devseed
//	go run ./cmd/devseed -password '...' -tenant-email demo@example.invalid
//
// DB_URL must be a connection role that is a member of altserv_app, and
// PATIENT_ENCRYPTION_KEY / PATIENT_SEARCH_KEY must be set, because patient data
// is encrypted and blind-indexed.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/HunterRedou/altserv/internal/config"
	"github.com/HunterRedou/altserv/internal/devseed"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("devseed: configuration: %v", err)
	}
	if err := devseed.EnsureDevelopment(cfg.Platform); err != nil {
		log.Fatalf("devseed: %v", err)
	}

	tenantEmail := flag.String("tenant-email", "demo-tenant@demo.invalid", "email of the demo tenant")
	tenantName := flag.String("tenant-name", "Demo Pflegedienst", "display name of the demo tenant")
	password := flag.String("password", "", "password for the three demo users (or set DEV_SEED_PASSWORD)")
	flag.Parse()

	if *password == "" {
		*password = os.Getenv("DEV_SEED_PASSWORD")
	}
	if *password == "" {
		log.Fatal("devseed: -password or DEV_SEED_PASSWORD is required")
	}

	conn, err := sql.Open("pgx", cfg.DBURL)
	if err != nil {
		log.Fatalf("devseed: open database: %v", err)
	}
	defer conn.Close()

	if err := conn.Ping(); err != nil {
		log.Fatalf("devseed: ping database: %v", err)
	}

	result, err := devseed.Run(context.Background(), conn, devseed.Options{
		Platform:                cfg.Platform,
		TenantEmail:             *tenantEmail,
		TenantName:              *tenantName,
		Password:                *password,
		PatientEncryptionKey:    cfg.PatientEncryptionKey,
		PatientKeyVersion:       cfg.PatientKeyVersion,
		PatientSearchKey:        cfg.PatientSearchKey,
		PatientSearchKeyVersion: cfg.PatientSearchKeyVersion,
	})
	if err != nil {
		log.Fatalf("devseed: %v", err)
	}

	fmt.Println("created synthetic development data:")
	fmt.Printf("  tenant:        %s (%s)\n", result.TenantID, *tenantEmail)
	fmt.Printf("  chef:          %s (%s)\n", result.ChefID, devseed.ChefEmail)
	fmt.Printf("  employee:      %s (%s)\n", result.EmployeeID, devseed.EmployeeEmail)
	fmt.Printf("  platform admin: %s (%s)\n", result.AdminID, devseed.AdminEmail)
	fmt.Printf("  catalog:       %s (%s)\n", result.CatalogID, devseed.CatalogCode)
	fmt.Printf("  definitions:   %d\n", len(result.DefinitionIDs))
	for _, id := range result.DefinitionIDs {
		fmt.Printf("    %s\n", id)
	}
	fmt.Printf("  patients:      %d\n", len(result.PatientIDs))
	for _, id := range result.PatientIDs {
		fmt.Printf("    %s\n", id)
	}
	fmt.Printf("  template:      %s\n", result.TemplateID)
	fmt.Printf("  planned visit: %s\n", result.VisitID)
	fmt.Println()
	fmt.Println("The data is synthetic. Do not treat the catalog codes as real Brandenburg codes.")
}
