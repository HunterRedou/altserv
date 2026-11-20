package tenantadmin

import (
	"context"
	"errors"
	"testing"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

func TestParseAction(t *testing.T) {
	for _, value := range []string{"activate", "suspend"} {
		if _, err := ParseAction(value); err != nil {
			t.Errorf("ParseAction(%q) = %v, want nil", value, err)
		}
	}
	for _, value := range []string{"", "delete", "terminate", "Activate"} {
		if _, err := ParseAction(value); !errors.Is(err, ErrInvalidAction) {
			t.Errorf("ParseAction(%q) = %v, want ErrInvalidAction", value, err)
		}
	}
}

func TestApplyRejectsInvalidActionBeforeDatabase(t *testing.T) {
	run := func(context.Context, uuid.UUID, func(*db.Queries) error) error {
		t.Fatal("runner must not be called for an invalid action")
		return nil
	}
	_, err := Apply(context.Background(), run, audit.NoopRecorder{}, uuid.New(), Action("delete"), "req")
	if !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("Apply error = %v, want ErrInvalidAction", err)
	}
}
