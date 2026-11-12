package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

type fakeStore struct {
	catalogs    []Catalog
	catalogsErr error

	catalog    Catalog
	catalogErr error

	createCatalogCalls int
	lastCatalogInput   CreateCatalogInput
	catalogCreateErr   error

	createDefinitionCalls int
	lastDefinitionInput   CreateDefinitionInput
	definitionCreateErr   error

	definitions    []Definition
	definitionsErr error
	lastFilter     DefinitionFilter
	lastLimit      int

	definition    Definition
	definitionErr error

	contract    Contract
	contractErr error

	contracts    []Contract
	contractsErr error

	createCalls int
	lastTenant  uuid.UUID
	lastInput   CreateContractInput

	rule           Rule
	ruleErr        error
	rules          []Rule
	rulesErr       error
	ruleCalls      int
	lastRule       CreateRuleInput
	lastRuleTenant uuid.UUID
}

func (f *fakeStore) ListCatalogs(context.Context, *db.Queries, bool) ([]Catalog, error) {
	return f.catalogs, f.catalogsErr
}

func (f *fakeStore) GetCatalog(context.Context, *db.Queries, uuid.UUID) (Catalog, error) {
	return f.catalog, f.catalogErr
}

func (f *fakeStore) CreateCatalog(_ context.Context, _ *db.Queries, input CreateCatalogInput) (Catalog, error) {
	f.createCatalogCalls++
	f.lastCatalogInput = input
	return f.catalog, f.catalogCreateErr
}

func (f *fakeStore) CreateDefinition(_ context.Context, _ *db.Queries, input CreateDefinitionInput) (Definition, error) {
	f.createDefinitionCalls++
	f.lastDefinitionInput = input
	return f.definition, f.definitionCreateErr
}

func (f *fakeStore) SearchDefinitions(_ context.Context, _ *db.Queries, filter DefinitionFilter, limit int) ([]Definition, error) {
	f.lastFilter = filter
	f.lastLimit = limit
	return f.definitions, f.definitionsErr
}

func (f *fakeStore) GetDefinition(context.Context, *db.Queries, uuid.UUID) (Definition, error) {
	return f.definition, f.definitionErr
}

func (f *fakeStore) CreateContract(_ context.Context, _ *db.Queries, tenantID uuid.UUID, input CreateContractInput) (Contract, error) {
	f.createCalls++
	f.lastTenant = tenantID
	f.lastInput = input
	return f.contract, f.contractErr
}

func (f *fakeStore) GetContract(context.Context, *db.Queries, uuid.UUID, uuid.UUID) (Contract, error) {
	return f.contract, f.contractErr
}

func (f *fakeStore) ListContracts(context.Context, *db.Queries, uuid.UUID) ([]Contract, error) {
	return f.contracts, f.contractsErr
}

func (f *fakeStore) CreateRule(_ context.Context, _ *db.Queries, tenantID uuid.UUID, input CreateRuleInput) (Rule, error) {
	f.ruleCalls++
	f.lastRuleTenant = tenantID
	f.lastRule = input
	return f.rule, f.ruleErr
}

func (f *fakeStore) ListRules(context.Context, *db.Queries, uuid.UUID) ([]Rule, error) {
	return f.rules, f.rulesErr
}

type fakeAudit struct {
	events []audit.Event
}

func (f *fakeAudit) Record(_ context.Context, _ *db.Queries, event audit.Event) error {
	f.events = append(f.events, event)
	return nil
}

func activeDefinition() Definition {
	return Definition{
		ID:        uuid.New(),
		Code:      "LK-001",
		Name:      "Grundpflege",
		LegalArea: "sgb_xi",
		ValidFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestServiceCatalogReadAccess(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store, &fakeAudit{})

	// Global reference catalogs are readable by every authenticated role. They
	// are not patient data, so platform_admin may read them (doc 4).
	for _, role := range []string{"chef", "employee", "platform_admin"} {
		t.Run(role, func(t *testing.T) {
			actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: role}
			if _, err := service.ListCatalogs(context.Background(), nil, actor, false); err != nil {
				t.Fatalf("ListCatalogs: %v", err)
			}
			if _, err := service.GetCatalog(context.Background(), nil, actor, uuid.New()); err != nil {
				t.Fatalf("GetCatalog: %v", err)
			}
			if _, err := service.SearchDefinitions(context.Background(), nil, actor, DefinitionFilter{}); err != nil {
				t.Fatalf("SearchDefinitions: %v", err)
			}
			if _, err := service.GetDefinition(context.Background(), nil, actor, uuid.New()); err != nil {
				t.Fatalf("GetDefinition: %v", err)
			}
		})
	}

	for _, role := range []string{"unknown", ""} {
		t.Run("denied_"+role, func(t *testing.T) {
			recorder := &fakeAudit{}
			service := NewService(store, recorder)
			actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: role}
			if _, err := service.ListCatalogs(context.Background(), nil, actor, false); !errors.Is(err, ErrForbidden) {
				t.Fatalf("ListCatalogs err = %v, want ErrForbidden", err)
			}
			if _, err := service.GetCatalog(context.Background(), nil, actor, uuid.New()); !errors.Is(err, ErrForbidden) {
				t.Fatalf("GetCatalog err = %v, want ErrForbidden", err)
			}
			if _, err := service.SearchDefinitions(context.Background(), nil, actor, DefinitionFilter{}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("SearchDefinitions err = %v, want ErrForbidden", err)
			}
			if _, err := service.GetDefinition(context.Background(), nil, actor, uuid.New()); !errors.Is(err, ErrForbidden) {
				t.Fatalf("GetDefinition err = %v, want ErrForbidden", err)
			}
			if len(recorder.events) != 0 {
				t.Errorf("audit events = %d, want none for a read-only denial", len(recorder.events))
			}
		})
	}
}

func TestServiceTenantCatalogReadAccessExcludesPlatformAdmin(t *testing.T) {
	// Tenant-bound contracts and rules stay tenant data. A platform admin gets
	// no automatic access, even though it may read global catalogs.
	for _, role := range []string{"platform_admin", "unknown"} {
		t.Run(role, func(t *testing.T) {
			service := NewService(&fakeStore{}, &fakeAudit{})
			tenantID := uuid.New()
			actor := Actor{UserID: uuid.New(), TenantID: tenantID, Role: role}
			if _, err := service.ListContracts(context.Background(), nil, actor); !errors.Is(err, ErrForbidden) {
				t.Fatalf("ListContracts err = %v, want ErrForbidden", err)
			}
			if _, err := service.GetContract(context.Background(), nil, actor, uuid.New()); !errors.Is(err, ErrForbidden) {
				t.Fatalf("GetContract err = %v, want ErrForbidden", err)
			}
			if _, err := service.ListRules(context.Background(), nil, actor); !errors.Is(err, ErrForbidden) {
				t.Fatalf("ListRules err = %v, want ErrForbidden", err)
			}
		})
	}
}

func TestServiceSearchDefinitions(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store, &fakeAudit{})

	_, err := service.SearchDefinitions(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}, DefinitionFilter{
		LegalArea: "sgb_xi",
		Query:     "  Grund   Pflege ",
	})
	if err != nil {
		t.Fatalf("SearchDefinitions: %v", err)
	}
	if store.lastFilter.Query != "Grund Pflege" {
		t.Errorf("query = %q, want normalized whitespace", store.lastFilter.Query)
	}
	if store.lastFilter.LegalArea != "sgb_xi" {
		t.Errorf("legal area = %q, want sgb_xi", store.lastFilter.LegalArea)
	}
	if store.lastLimit != maxDefinitionResults {
		t.Errorf("limit = %d, want %d", store.lastLimit, maxDefinitionResults)
	}
}

func TestServiceSearchDefinitionsRejectsBadInput(t *testing.T) {
	service := NewService(&fakeStore{}, &fakeAudit{})
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}

	if _, err := service.SearchDefinitions(context.Background(), nil, actor, DefinitionFilter{LegalArea: "sgb_xii"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid legal area err = %v, want ErrInvalid", err)
	}
	if _, err := service.SearchDefinitions(context.Background(), nil, actor, DefinitionFilter{Query: strings.Repeat("a", maxDefinitionQueryRunes+1)}); !errors.Is(err, ErrInvalid) {
		t.Errorf("long query err = %v, want ErrInvalid", err)
	}
}

func TestServiceCreateContractRequiresChef(t *testing.T) {
	for _, role := range []string{"employee", "platform_admin"} {
		t.Run(role, func(t *testing.T) {
			store := &fakeStore{}
			recorder := &fakeAudit{}
			service := NewService(store, recorder)
			tenantID := uuid.New()

			_, err := service.CreateContract(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: role}, "req", CreateContractInput{
				ServiceDefinitionID: uuid.New(),
				PayerName:           "AOK",
				LegalArea:           "sgb_xi",
				ValidFrom:           time.Now(),
			})
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if store.createCalls != 0 {
				t.Error("store was called for a forbidden contract")
			}
			if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
				t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
			}
		})
	}
}

func TestServiceCreateContractValidatesInput(t *testing.T) {
	definition := activeDefinition()
	service := NewService(&fakeStore{definition: definition}, &fakeAudit{})
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}
	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	validUntil := validFrom.Add(-time.Hour)

	tests := []struct {
		name  string
		input CreateContractInput
	}{
		{name: "missing definition", input: CreateContractInput{PayerName: "AOK", LegalArea: "sgb_xi", ValidFrom: validFrom}},
		{name: "missing payer", input: CreateContractInput{ServiceDefinitionID: definition.ID, LegalArea: "sgb_xi", ValidFrom: validFrom}},
		{name: "invalid legal area", input: CreateContractInput{ServiceDefinitionID: definition.ID, PayerName: "AOK", LegalArea: "sgb_xii", ValidFrom: validFrom}},
		{name: "missing valid from", input: CreateContractInput{ServiceDefinitionID: definition.ID, PayerName: "AOK", LegalArea: "sgb_xi"}},
		{name: "valid until before valid from", input: CreateContractInput{ServiceDefinitionID: definition.ID, PayerName: "AOK", LegalArea: "sgb_xi", ValidFrom: validFrom, ValidUntil: &validUntil}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := service.CreateContract(context.Background(), nil, actor, "", tt.input); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestServiceCreateContractEnforcesLegalAreaAndDefinitionWindow(t *testing.T) {
	definition := activeDefinition()
	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}

	t.Run("legal area mismatch", func(t *testing.T) {
		store := &fakeStore{definition: definition}
		service := NewService(store, &fakeAudit{})

		_, err := service.CreateContract(context.Background(), nil, actor, "", CreateContractInput{
			ServiceDefinitionID: definition.ID,
			PayerName:           "AOK",
			LegalArea:           "sgb_v",
			ValidFrom:           validFrom,
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if store.createCalls != 0 {
			t.Error("store was called for a mismatched legal area")
		}
	})

	t.Run("definition expired", func(t *testing.T) {
		expired := definition
		until := validFrom.Add(-24 * time.Hour)
		expired.ValidUntil = &until
		store := &fakeStore{definition: expired}
		service := NewService(store, &fakeAudit{})

		_, err := service.CreateContract(context.Background(), nil, actor, "", CreateContractInput{
			ServiceDefinitionID: definition.ID,
			PayerName:           "AOK",
			LegalArea:           "sgb_xi",
			ValidFrom:           validFrom,
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
}

func TestServiceCreateContract(t *testing.T) {
	definition := activeDefinition()
	contractID := uuid.New()
	tenantID := uuid.New()
	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &fakeStore{
		definition: definition,
		contract: Contract{
			ID:                  contractID,
			TenantID:            tenantID,
			ServiceDefinitionID: definition.ID,
			PayerName:           "AOK Brandenburg",
			ContractReference:   "V-2026-1",
			LegalArea:           "sgb_xi",
			ValidFrom:           validFrom,
		},
	}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)

	contract, err := service.CreateContract(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "req-contract", CreateContractInput{
		ServiceDefinitionID: definition.ID,
		PayerName:           "  AOK Brandenburg ",
		ContractReference:   " V-2026-1 ",
		LegalArea:           "sgb_xi",
		ValidFrom:           validFrom,
	})
	if err != nil {
		t.Fatalf("CreateContract: %v", err)
	}
	if contract.ID != contractID {
		t.Errorf("contract id = %s, want %s", contract.ID, contractID)
	}
	if store.lastTenant != tenantID {
		t.Errorf("tenant = %s, want %s", store.lastTenant, tenantID)
	}
	if store.lastInput.PayerName != "AOK Brandenburg" || store.lastInput.ContractReference != "V-2026-1" {
		t.Errorf("input was not trimmed: %+v", store.lastInput)
	}

	if len(recorder.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Action != "service_contract.created" || event.EntityID != contractID {
		t.Errorf("event = %+v", event)
	}
	if _, ok := event.Metadata["payer_name"]; ok {
		t.Error("audit metadata contains the payer name")
	}
	if event.Metadata["service_code"] != definition.Code {
		t.Errorf("service_code = %v, want %s", event.Metadata["service_code"], definition.Code)
	}
}

func activeContract(definition Definition, tenantID uuid.UUID) Contract {
	return Contract{
		ID:                  uuid.New(),
		TenantID:            tenantID,
		ServiceDefinitionID: definition.ID,
		PayerName:           "AOK Brandenburg",
		ContractReference:   "V-2026-1",
		LegalArea:           definition.LegalArea,
		ValidFrom:           time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestServiceCreateRuleRequiresChef(t *testing.T) {
	for _, role := range []string{"employee", "platform_admin"} {
		t.Run(role, func(t *testing.T) {
			store := &fakeStore{}
			recorder := &fakeAudit{}
			service := NewService(store, recorder)
			tenantID := uuid.New()

			_, err := service.CreateRule(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: role}, "req", CreateRuleInput{
				ServiceDefinitionID: uuid.New(),
				ActivityLabel:       "Grundpflege",
				LegalArea:           "sgb_xi",
				ValidFrom:           time.Now(),
			})
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
			if store.ruleCalls != 0 {
				t.Error("store was called for a forbidden rule")
			}
			if len(recorder.events) != 1 || recorder.events[0].Action != "access.denied" {
				t.Fatalf("audit events = %+v, want one access.denied", recorder.events)
			}
		})
	}
}

func TestServiceCreateRuleValidatesInput(t *testing.T) {
	definition := activeDefinition()
	service := NewService(&fakeStore{definition: definition}, &fakeAudit{})
	actor := Actor{UserID: uuid.New(), TenantID: uuid.New(), Role: "chef"}
	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	validUntil := validFrom.Add(-time.Hour)

	tests := []struct {
		name  string
		input CreateRuleInput
	}{
		{name: "missing definition", input: CreateRuleInput{ActivityLabel: "Pflege", LegalArea: "sgb_xi", ValidFrom: validFrom}},
		{name: "missing label", input: CreateRuleInput{ServiceDefinitionID: definition.ID, LegalArea: "sgb_xi", ValidFrom: validFrom}},
		{name: "long label", input: CreateRuleInput{ServiceDefinitionID: definition.ID, ActivityLabel: strings.Repeat("a", maxActivityLabelRunes+1), LegalArea: "sgb_xi", ValidFrom: validFrom}},
		{name: "invalid legal area", input: CreateRuleInput{ServiceDefinitionID: definition.ID, ActivityLabel: "Pflege", LegalArea: "sgb_xii", ValidFrom: validFrom}},
		{name: "missing valid from", input: CreateRuleInput{ServiceDefinitionID: definition.ID, ActivityLabel: "Pflege", LegalArea: "sgb_xi"}},
		{name: "valid until before valid from", input: CreateRuleInput{ServiceDefinitionID: definition.ID, ActivityLabel: "Pflege", LegalArea: "sgb_xi", ValidFrom: validFrom, ValidUntil: &validUntil}},
		{name: "invalid criteria json", input: CreateRuleInput{ServiceDefinitionID: definition.ID, ActivityLabel: "Pflege", LegalArea: "sgb_xi", Criteria: json.RawMessage(`{`), ValidFrom: validFrom}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := service.CreateRule(context.Background(), nil, actor, "", tt.input); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestServiceCreateRuleEnforcesDefinitionAndContract(t *testing.T) {
	definition := activeDefinition()
	tenantID := uuid.New()
	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	actor := Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}

	t.Run("legal area mismatch", func(t *testing.T) {
		store := &fakeStore{definition: definition}
		service := NewService(store, &fakeAudit{})

		_, err := service.CreateRule(context.Background(), nil, actor, "", CreateRuleInput{
			ServiceDefinitionID: definition.ID,
			ActivityLabel:       "Pflege",
			LegalArea:           "sgb_v",
			ValidFrom:           validFrom,
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if store.ruleCalls != 0 {
			t.Error("store was called for a mismatched legal area")
		}
	})

	t.Run("definition expired", func(t *testing.T) {
		expired := definition
		until := validFrom.Add(-24 * time.Hour)
		expired.ValidUntil = &until
		store := &fakeStore{definition: expired}
		service := NewService(store, &fakeAudit{})

		_, err := service.CreateRule(context.Background(), nil, actor, "", CreateRuleInput{
			ServiceDefinitionID: definition.ID,
			ActivityLabel:       "Pflege",
			LegalArea:           "sgb_xi",
			ValidFrom:           validFrom,
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("contract from another service", func(t *testing.T) {
		other := activeContract(definition, tenantID)
		other.ServiceDefinitionID = uuid.New()
		store := &fakeStore{definition: definition, contract: other}
		service := NewService(store, &fakeAudit{})

		contractID := other.ID
		_, err := service.CreateRule(context.Background(), nil, actor, "", CreateRuleInput{
			ServiceDefinitionID: definition.ID,
			ContractID:          &contractID,
			ActivityLabel:       "Pflege",
			LegalArea:           "sgb_xi",
			ValidFrom:           validFrom,
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if store.ruleCalls != 0 {
			t.Error("store was called for a mismatched contract")
		}
	})
}

func TestServiceCreateRule(t *testing.T) {
	definition := activeDefinition()
	tenantID := uuid.New()
	contract := activeContract(definition, tenantID)
	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ruleID := uuid.New()

	store := &fakeStore{
		definition: definition,
		contract:   contract,
		rule: Rule{
			ID:                  ruleID,
			TenantID:            tenantID,
			ServiceDefinitionID: definition.ID,
			ContractID:          &contract.ID,
			ActivityLabel:       "Grundpflege",
			LegalArea:           "sgb_xi",
			Criteria:            json.RawMessage(`{"min_duration":15}`),
			ValidFrom:           validFrom,
		},
	}
	recorder := &fakeAudit{}
	service := NewService(store, recorder)

	rule, err := service.CreateRule(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "chef"}, "req-rule", CreateRuleInput{
		ServiceDefinitionID: definition.ID,
		ContractID:          &contract.ID,
		ActivityLabel:       "  Grundpflege  ",
		LegalArea:           "sgb_xi",
		Criteria:            json.RawMessage(`{"min_duration":15}`),
		ValidFrom:           validFrom,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if rule.ID != ruleID {
		t.Errorf("rule id = %s, want %s", rule.ID, ruleID)
	}
	if store.lastRuleTenant != tenantID {
		t.Errorf("tenant = %s, want %s", store.lastRuleTenant, tenantID)
	}
	if store.lastRule.ActivityLabel != "Grundpflege" {
		t.Errorf("label = %q, want trimmed label", store.lastRule.ActivityLabel)
	}

	if len(recorder.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Action != "activity_rule.created" || event.EntityID != ruleID {
		t.Errorf("event = %+v", event)
	}
	if event.Metadata["service_code"] != definition.Code {
		t.Errorf("service_code = %v, want %s", event.Metadata["service_code"], definition.Code)
	}
	if event.Metadata["has_contract"] != true {
		t.Errorf("has_contract = %v, want true", event.Metadata["has_contract"])
	}
}

func TestServiceListRulesAccess(t *testing.T) {
	tenantID := uuid.New()

	t.Run("chef and employee may list", func(t *testing.T) {
		for _, role := range []string{"chef", "employee"} {
			store := &fakeStore{rules: []Rule{{ID: uuid.New(), TenantID: tenantID}}}
			service := NewService(store, &fakeAudit{})
			rules, err := service.ListRules(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: role})
			if err != nil {
				t.Fatalf("%s ListRules: %v", role, err)
			}
			if len(rules) != 1 {
				t.Fatalf("%s rules = %d, want 1", role, len(rules))
			}
		}
	})

	t.Run("platform admin denied", func(t *testing.T) {
		service := NewService(&fakeStore{}, &fakeAudit{})
		if _, err := service.ListRules(context.Background(), nil, Actor{UserID: uuid.New(), TenantID: tenantID, Role: "platform_admin"}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})
}
