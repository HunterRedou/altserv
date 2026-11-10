package audit

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestSQLRecorderValidatesEvent(t *testing.T) {
	tests := []struct {
		name  string
		event Event
	}{
		{name: "missing tenant", event: Event{Action: "a", EntityType: "patient"}},
		{name: "missing action", event: Event{TenantID: uuid.New(), EntityType: "patient"}},
		{name: "missing entity type", event: Event{TenantID: uuid.New(), Action: "a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Validation must happen before the nil *db.Queries is used.
			if err := (SQLRecorder{}).Record(context.Background(), nil, tt.event); err == nil {
				t.Fatal("Record returned nil error")
			}
		})
	}
}
