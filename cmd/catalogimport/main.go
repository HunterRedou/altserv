// Command catalogimport loads one professionally verified catalog version and
// its billable services.
//
// It is an operator tool, not an HTTP route. The manifest must carry a source
// note, so an unverified or invented catalog cannot be loaded (doc 9). The
// whole import runs in one transaction: either the catalog and every service
// are written, or nothing is.
//
// Usage:
//
//	go run ./cmd/catalogimport -file catalog.json -tenant <operator-tenant-uuid>
//	go run ./cmd/catalogimport -file catalog.json -tenant <uuid> -dry-run
//
// The connection uses DB_URL and must be a member of the altserv_app role,
// because the import runs inside the same tenant-scoped transaction as the
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
	"github.com/HunterRedou/altserv/internal/catalog"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	file := flag.String("file", "", "path to the catalog manifest JSON")
	tenant := flag.String("tenant", "", "operator tenant UUID used for the audit event")
	dryRun := flag.Bool("dry-run", false, "validate the manifest and print a summary, write nothing")
	flag.Parse()

	if *file == "" {
		log.Fatal("catalogimport: -file is required")
	}
	if *tenant == "" {
		log.Fatal("catalogimport: -tenant is required")
	}

	tenantID, err := uuid.Parse(*tenant)
	if err != nil {
		log.Fatalf("catalogimport: -tenant must be a UUID: %v", err)
	}

	data, err := os.ReadFile(*file)
	if err != nil {
		log.Fatalf("catalogimport: read manifest: %v", err)
	}

	manifest, err := catalog.ParseManifest(data)
	if err != nil {
		log.Fatalf("catalogimport: %v", err)
	}

	plan, err := manifest.Plan()
	if err != nil {
		log.Fatalf("catalogimport: %v", err)
	}

	label := plan.CatalogID.String()
	if plan.Catalog != nil {
		label = plan.Catalog.Code
	}

	if *dryRun {
		fmt.Printf("manifest OK: catalog %s with %d definitions (dry run)\n", label, len(plan.Definitions))
		return
	}

	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		log.Fatal("catalogimport: DB_URL is required")
	}

	conn, err := sql.Open("pgx", dbURL)
	if err != nil {
		log.Fatalf("catalogimport: open database: %v", err)
	}
	defer conn.Close()

	if err := conn.Ping(); err != nil {
		log.Fatalf("catalogimport: ping database: %v", err)
	}

	service := catalog.NewService(catalog.NewRepository(), audit.SQLRecorder{})
	actor := catalog.Actor{TenantID: tenantID, Role: "platform_admin"}

	var result catalog.ImportResult
	err = db.WithTenant(context.Background(), conn, tenantID, func(q *db.Queries) error {
		var err error
		result, err = service.ImportManifest(context.Background(), q, actor, uuid.NewString(), manifest)
		return err
	})
	if err != nil {
		log.Fatalf("catalogimport: %v", err)
	}

	fmt.Printf(
		"imported catalog %s (%s) with %d definitions\n",
		result.Catalog.Code,
		result.Catalog.ID,
		len(result.Definitions),
	)
}
