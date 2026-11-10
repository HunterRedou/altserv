package schema_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrationsWrapDollarQuotedStatements is a regression test for a goose
// error we hit in 012_row_level_security.sql: goose splits on semicolons, so a
// DO $$ ... $$; block must be wrapped in StatementBegin/StatementEnd. Without
// the wrapper goose fails with "unterminated dollar-quoted string".
func TestMigrationsWrapDollarQuotedStatements(t *testing.T) {
	files, err := filepath.Glob("*.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no migration files found")
	}

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read migration: %v", err)
			}
			content := string(data)

			if !strings.Contains(content, "-- +goose Up") {
				t.Error("migration has no '-- +goose Up' section")
			}
			if !strings.Contains(content, "-- +goose Down") {
				t.Error("migration has no '-- +goose Down' section")
			}

			inStatement := false
			for lineNumber, line := range strings.Split(content, "\n") {
				trimmed := strings.TrimSpace(line)
				switch trimmed {
				case "-- +goose StatementBegin":
					inStatement = true
					continue
				case "-- +goose StatementEnd":
					inStatement = false
					continue
				}

				if strings.Contains(line, "$$") && !inStatement {
					t.Errorf("line %d uses a dollar-quoted block outside StatementBegin/StatementEnd: %s", lineNumber+1, trimmed)
				}
			}
		})
	}
}
