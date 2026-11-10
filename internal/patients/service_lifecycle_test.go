package patients

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestServiceCreateWritesSearchTokens(t *testing.T) {
	tenantID, patientID := uuid.New(), uuid.New()
	indexer := newTestIndexer(t)
	store := &fakeStore{createPatient: Patient{
		ID:        patientID,
		TenantID:  tenantID,
		FirstName: "Anna",
		LastName:  "Muster",
		Status:    "active",
	}}
	service := NewServiceWithSearch(store, &fakeAudit{}, nil, indexer)
	service.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

	if _, err := service.Create(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", CreateInput{
		FirstName: "Anna",
		LastName:  "Muster",
		BirthDate: time.Date(1950, 5, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if store.tokenCalls != 1 {
		t.Fatalf("ReplaceSearchTokens calls = %d, want 1", store.tokenCalls)
	}
	if store.lastTokenTenant != tenantID || store.lastTokenPatient != patientID {
		t.Errorf("token tenant/patient = %s/%s", store.lastTokenTenant, store.lastTokenPatient)
	}
	assertTokensContain(t, store.lastTokens, indexer.HashString(tenantID, "anna"))
	assertTokensContain(t, store.lastTokens, indexer.HashString(tenantID, "muster"))
}

func TestServiceUpdate(t *testing.T) {
	tenantID, chefID, patientID := uuid.New(), uuid.New(), uuid.New()
	indexer := newTestIndexer(t)
	existing := Patient{
		ID:        patientID,
		TenantID:  tenantID,
		FirstName: "Anna",
		LastName:  "Muster",
		BirthDate: time.Date(1950, 5, 1, 0, 0, 0, 0, time.UTC),
		Status:    "active",
	}
	updated := existing
	updated.LastName = "Schmidt"
	store := &fakeStore{getPatient: existing, updatePatient: updated}
	recorder := &fakeAudit{}
	service := NewServiceWithSearch(store, recorder, nil, indexer)
	service.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

	newName := "Schmidt"
	patient, err := service.Update(context.Background(), nil, Actor{UserID: chefID, TenantID: tenantID, Role: "chef"}, "req-update", patientID, UpdateInput{
		LastName: &newName,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if patient.LastName != "Schmidt" {
		t.Errorf("last name = %q, want Schmidt", patient.LastName)
	}
	if store.updateCalls != 1 || store.lastUpdateID != patientID {
		t.Errorf("store.Update calls = %d, id = %s", store.updateCalls, store.lastUpdateID)
	}
	// Unspecified fields keep their old values and are validated as a whole.
	if store.lastUpdate.FirstName != "Anna" || store.lastUpdate.Status != "active" {
		t.Errorf("update input = %+v, want first name and status preserved", store.lastUpdate)
	}

	// The index is rebuilt from the new name, not appended to the old one.
	if store.tokenCalls != 1 {
		t.Fatalf("ReplaceSearchTokens calls = %d, want 1", store.tokenCalls)
	}
	assertTokensContain(t, store.lastTokens, indexer.HashString(tenantID, "schmidt"))
	assertTokensDoNotContain(t, store.lastTokens, indexer.HashString(tenantID, "muster"))

	if len(recorder.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Action != "patient.updated" {
		t.Errorf("action = %q, want patient.updated", event.Action)
	}
	changed, ok := event.Metadata["changed_fields"].([]string)
	if !ok || len(changed) != 1 || changed[0] != "last_name" {
		t.Errorf("changed_fields = %#v, want [last_name]", event.Metadata["changed_fields"])
	}
	if _, ok := event.Metadata["last_name"]; ok {
		t.Error("audit metadata contains the patient name")
	}
}

func TestServiceUpdateForbidden(t *testing.T) {
	for _, role := range []string{"employee", "platform_admin"} {
		t.Run(role, func(t *testing.T) {
			store := &fakeStore{}
			recorder := &fakeAudit{}
			service := NewServiceWithSearch(store, recorder, nil, newTestIndexer(t))

			name := "Berta"
			_, err := service.Update(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: role}, "req", uuid.New(), UpdateInput{FirstName: &name})
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if store.updateCalls != 0 {
				t.Error("store was called for a forbidden update")
			}
			if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
				t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
			}
		})
	}
}

func TestServiceUpdateRejectsEmptyAndInvalidChanges(t *testing.T) {
	tenantID, patientID := uuid.New(), uuid.New()
	existing := Patient{
		ID:        patientID,
		TenantID:  tenantID,
		FirstName: "Anna",
		LastName:  "Muster",
		BirthDate: time.Date(1950, 5, 1, 0, 0, 0, 0, time.UTC),
		Status:    "active",
	}

	t.Run("no fields", func(t *testing.T) {
		store := &fakeStore{getPatient: existing}
		service := NewServiceWithSearch(store, &fakeAudit{}, nil, newTestIndexer(t))

		_, err := service.Update(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", patientID, UpdateInput{})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if store.updateCalls != 0 {
			t.Error("store was called for an empty update")
		}
	})

	t.Run("empty name", func(t *testing.T) {
		store := &fakeStore{getPatient: existing}
		service := NewServiceWithSearch(store, &fakeAudit{}, nil, newTestIndexer(t))

		empty := "   "
		_, err := service.Update(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "", patientID, UpdateInput{FirstName: &empty})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if store.updateCalls != 0 {
			t.Error("store was called for an invalid update")
		}
	})
}

func TestServiceSearchChefUsesTenantWideQuery(t *testing.T) {
	tenantID := uuid.New()
	indexer := newTestIndexer(t)
	found := Patient{ID: uuid.New(), TenantID: tenantID}
	store := &fakeStore{searchPatients: []Patient{found}}
	recorder := &fakeAudit{}
	service := NewServiceWithSearch(store, recorder, nil, indexer)

	patients, err := service.Search(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "req-search", "Anna")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(patients) != 1 || patients[0].ID != found.ID {
		t.Fatalf("patients = %+v, want the store result", patients)
	}
	if store.lastSearchEmployee != nil {
		t.Errorf("employee filter = %s, want nil for a chef", *store.lastSearchEmployee)
	}
	if store.lastSearchLimit != maxSearchResults {
		t.Errorf("limit = %d, want %d", store.lastSearchLimit, maxSearchResults)
	}
	wantHashes, _ := indexer.QueryHashes(tenantID, "Anna")
	if len(store.lastSearchHashes) != len(wantHashes) || store.lastSearchHashes[0] != wantHashes[0] {
		t.Errorf("hashes = %v, want %v", store.lastSearchHashes, wantHashes)
	}

	if len(recorder.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Action != "patient.searched" {
		t.Errorf("action = %q, want patient.searched", event.Action)
	}
	if event.Metadata["terms"] != 1 || event.Metadata["results"] != 1 {
		t.Errorf("metadata = %+v, want terms/results counts", event.Metadata)
	}
	for _, value := range event.Metadata {
		if text, ok := value.(string); ok && text == "Anna" {
			t.Fatal("audit metadata contains the raw search term")
		}
	}
}

func TestServiceSearchEmployeeIsAssignmentScoped(t *testing.T) {
	tenantID, employeeID := uuid.New(), uuid.New()
	store := &fakeStore{}
	service := NewServiceWithSearch(store, &fakeAudit{}, nil, newTestIndexer(t))

	if _, err := service.Search(context.Background(), nil, Actor{UserID: employeeID, TenantID: tenantID, Role: "employee"}, "req", "Muster"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if store.lastSearchEmployee == nil || *store.lastSearchEmployee != employeeID {
		t.Fatalf("employee filter = %v, want %s", store.lastSearchEmployee, employeeID)
	}
}

func TestServiceSearchForbidden(t *testing.T) {
	for _, role := range []string{"platform_admin", "unknown"} {
		t.Run(role, func(t *testing.T) {
			store := &fakeStore{}
			recorder := &fakeAudit{}
			service := NewServiceWithSearch(store, recorder, nil, newTestIndexer(t))

			_, err := service.Search(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: role}, "req", "Anna")
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if store.searchCalls != 0 {
				t.Error("store was called for a forbidden search")
			}
			if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
				t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
			}
		})
	}
}

func TestServiceSearchRequiresIndexer(t *testing.T) {
	service := NewService(&fakeStore{}, &fakeAudit{})

	if _, err := service.Search(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, "", "Anna"); err == nil {
		t.Fatal("Search succeeded without a search indexer")
	}
}

func TestServiceSearchRejectsLongQuery(t *testing.T) {
	service := NewServiceWithSearch(&fakeStore{}, &fakeAudit{}, nil, newTestIndexer(t))

	_, err := service.Search(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, "", string(make([]rune, maxSearchQueryRunes+1)))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func assertTokensContain(t *testing.T, tokens []SearchToken, hash string) {
	t.Helper()

	for _, token := range tokens {
		if hex.EncodeToString(token.Hash) == hash {
			return
		}
	}
	t.Errorf("token %s is missing from %d tokens", hash, len(tokens))
}

func assertTokensDoNotContain(t *testing.T, tokens []SearchToken, hash string) {
	t.Helper()

	for _, token := range tokens {
		if hex.EncodeToString(token.Hash) == hash {
			t.Errorf("stale token %s is still present", hash)
			return
		}
	}
}
