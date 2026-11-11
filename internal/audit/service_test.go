package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

type fakeStore struct {
	events    []EventRecord
	err       error
	gotTenant uuid.UUID
	gotFilter ListFilter
	calls     int
}

func (f *fakeStore) List(_ context.Context, _ *db.Queries, tenantID uuid.UUID, filter ListFilter) ([]EventRecord, error) {
	f.calls++
	f.gotTenant = tenantID
	f.gotFilter = filter
	return f.events, f.err
}

type fakeRecorder struct {
	events []Event
	err    error
}

func (f *fakeRecorder) Record(_ context.Context, _ *db.Queries, event Event) error {
	f.events = append(f.events, event)
	return f.err
}

func TestAuditListIsChefOnly(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()

	for _, role := range []string{"employee", "platform_admin", ""} {
		t.Run(role, func(t *testing.T) {
			store := &fakeStore{}
			recorder := &fakeRecorder{}
			service := NewService(store, recorder)

			_, err := service.List(ctx, nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: role}, "req", ListFilter{})
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if store.calls != 0 {
				t.Fatalf("store called %d times, want 0", store.calls)
			}
			if len(recorder.events) != 1 {
				t.Fatalf("audit events = %+v, want one denial", recorder.events)
			}
			denied := recorder.events[0]
			if denied.Action != "access.denied" || denied.TenantID != tenantID {
				t.Fatalf("denied event = %+v", denied)
			}
			if denied.Metadata["attempted_action"] != auditReadEntityLabel {
				t.Fatalf("denied metadata = %+v", denied.Metadata)
			}
		})
	}
}

func TestAuditListRecordsTheRead(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	actorID := uuid.New()

	store := &fakeStore{events: []EventRecord{{ID: uuid.New(), TenantID: tenantID, Action: "patient.created"}}}
	recorder := &fakeRecorder{}
	service := NewService(store, recorder)

	events, err := service.List(ctx, nil, Actor{UserID: actorID, TenantID: tenantID, Role: "chef"}, "req-1", ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one", events)
	}
	if store.gotTenant != tenantID {
		t.Fatalf("tenant = %s, want %s", store.gotTenant, tenantID)
	}
	if store.gotFilter.Limit != defaultAuditLimit {
		t.Fatalf("limit = %d, want %d", store.gotFilter.Limit, defaultAuditLimit)
	}
	if len(recorder.events) != 1 {
		t.Fatalf("audit events = %+v, want one", recorder.events)
	}
	listed := recorder.events[0]
	if listed.Action != "audit.listed" || listed.ActorUserID != actorID {
		t.Fatalf("listed event = %+v", listed)
	}
	if listed.RequestID != "req-1" {
		t.Fatalf("request id = %q, want req-1", listed.RequestID)
	}
	if listed.Metadata["count"] != 1 {
		t.Fatalf("metadata = %+v, want count 1", listed.Metadata)
	}
}

func TestAuditListPassesFilters(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	entityID := uuid.New()
	from := time.Now().Add(-time.Hour)
	to := time.Now()

	store := &fakeStore{}
	service := NewService(store, &fakeRecorder{})
	filter := ListFilter{
		Action:     " patient.created ",
		EntityType: " patient ",
		EntityID:   &entityID,
		From:       &from,
		To:         &to,
		Limit:      25,
	}

	if _, err := service.List(ctx, nil, Actor{TenantID: tenantID, Role: "chef"}, "", filter); err != nil {
		t.Fatalf("List: %v", err)
	}
	if store.gotFilter.Action != "patient.created" {
		t.Fatalf("action = %q, want trimmed", store.gotFilter.Action)
	}
	if store.gotFilter.EntityType != "patient" {
		t.Fatalf("entity type = %q, want trimmed", store.gotFilter.EntityType)
	}
	if store.gotFilter.EntityID == nil || *store.gotFilter.EntityID != entityID {
		t.Fatalf("entity id = %v", store.gotFilter.EntityID)
	}
	if store.gotFilter.From == nil || !store.gotFilter.From.Equal(from) {
		t.Fatalf("from = %v", store.gotFilter.From)
	}
	if store.gotFilter.To == nil || !store.gotFilter.To.Equal(to) {
		t.Fatalf("to = %v", store.gotFilter.To)
	}
	if store.gotFilter.Limit != 25 {
		t.Fatalf("limit = %d, want 25", store.gotFilter.Limit)
	}
}

func TestAuditListRejectsInvalidFilters(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	now := time.Now()

	tests := []struct {
		name   string
		filter ListFilter
	}{
		{name: "action too long", filter: ListFilter{Action: string(make([]rune, maxAuditFilterRunes+1))}},
		{name: "entity type too long", filter: ListFilter{EntityType: string(make([]rune, maxAuditFilterRunes+1))}},
		{name: "negative limit", filter: ListFilter{Limit: -1}},
		{name: "limit too high", filter: ListFilter{Limit: maxAuditLimit + 1}},
		{name: "from equals to", filter: ListFilter{From: &now, To: &now}},
		{name: "from after to", filter: ListFilter{From: &now, To: func() *time.Time { earlier := now.Add(-time.Hour); return &earlier }()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			recorder := &fakeRecorder{}
			service := NewService(store, recorder)

			_, err := service.List(ctx, nil, Actor{TenantID: tenantID, Role: "chef"}, "", tt.filter)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if store.calls != 0 {
				t.Fatalf("store called %d times, want 0", store.calls)
			}
			if len(recorder.events) != 0 {
				t.Fatalf("audit events = %+v, want none", recorder.events)
			}
		})
	}
}

func TestAuditListWithoutRecorder(t *testing.T) {
	store := &fakeStore{events: []EventRecord{{ID: uuid.New()}}}
	service := NewService(store, nil)

	events, err := service.List(context.Background(), nil, Actor{TenantID: uuid.New(), Role: "chef"}, "", ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one", events)
	}
}

func TestAuditListPropagatesStoreError(t *testing.T) {
	wantErr := errors.New("boom")
	store := &fakeStore{err: wantErr}
	service := NewService(store, &fakeRecorder{})

	_, err := service.List(context.Background(), nil, Actor{TenantID: uuid.New(), Role: "chef"}, "", ListFilter{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}
