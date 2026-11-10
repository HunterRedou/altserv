package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Repository reads global reference data and writes tenant contracts.
type Repository struct{}

func NewRepository() *Repository {
	return &Repository{}
}

func (r *Repository) ListCatalogs(ctx context.Context, q *db.Queries, includeFuture bool) ([]Catalog, error) {
	rows, err := q.ListServiceCatalogs(ctx, includeFuture)
	if err != nil {
		return nil, fmt.Errorf("list service catalogs: %w", err)
	}
	catalogs := make([]Catalog, 0, len(rows))
	for _, row := range rows {
		catalogs = append(catalogs, toCatalog(row))
	}
	return catalogs, nil
}

func (r *Repository) GetCatalog(ctx context.Context, q *db.Queries, id uuid.UUID) (Catalog, error) {
	row, err := q.GetServiceCatalog(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Catalog{}, ErrNotFound
	}
	if err != nil {
		return Catalog{}, fmt.Errorf("get service catalog: %w", err)
	}
	return toCatalog(row), nil
}

func (r *Repository) SearchDefinitions(ctx context.Context, q *db.Queries, filter DefinitionFilter, limit int) ([]Definition, error) {
	catalogID := uuid.Nil
	if filter.CatalogID != nil {
		catalogID = *filter.CatalogID
	}

	rows, err := q.SearchServiceDefinitions(ctx, db.SearchServiceDefinitionsParams{
		CatalogID:  catalogID,
		LegalArea:  filter.LegalArea,
		Query:      filter.Query,
		MaxResults: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("search service definitions: %w", err)
	}
	definitions := make([]Definition, 0, len(rows))
	for _, row := range rows {
		definitions = append(definitions, toDefinition(row))
	}
	return definitions, nil
}

func (r *Repository) GetDefinition(ctx context.Context, q *db.Queries, id uuid.UUID) (Definition, error) {
	row, err := q.GetServiceDefinition(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Definition{}, ErrNotFound
	}
	if err != nil {
		return Definition{}, fmt.Errorf("get service definition: %w", err)
	}
	return toDefinition(row), nil
}

func (r *Repository) CreateContract(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input CreateContractInput) (Contract, error) {
	row, err := q.CreateTenantServiceContract(ctx, db.CreateTenantServiceContractParams{
		TenantID:            tenantID,
		ServiceDefinitionID: input.ServiceDefinitionID,
		PayerName:           input.PayerName,
		ContractReference:   input.ContractReference,
		LegalArea:           input.LegalArea,
		ValidFrom:           input.ValidFrom,
		ValidUntil:          nullableTime(input.ValidUntil),
	})
	if isUniqueViolation(err) {
		return Contract{}, ErrConflict
	}
	if err != nil {
		return Contract{}, fmt.Errorf("create service contract: %w", err)
	}
	return toContract(row), nil
}

func (r *Repository) GetContract(ctx context.Context, q *db.Queries, tenantID, id uuid.UUID) (Contract, error) {
	row, err := q.GetTenantServiceContract(ctx, db.GetTenantServiceContractParams{ID: id, TenantID: tenantID})
	if errors.Is(err, sql.ErrNoRows) {
		return Contract{}, ErrNotFound
	}
	if err != nil {
		return Contract{}, fmt.Errorf("get service contract: %w", err)
	}
	return toContract(row), nil
}

func (r *Repository) ListContracts(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Contract, error) {
	rows, err := q.ListTenantServiceContracts(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list service contracts: %w", err)
	}
	contracts := make([]Contract, 0, len(rows))
	for _, row := range rows {
		contracts = append(contracts, toContract(row))
	}
	return contracts, nil
}

func (r *Repository) CreateRule(ctx context.Context, q *db.Queries, tenantID uuid.UUID, input CreateRuleInput) (Rule, error) {
	criteria := input.Criteria
	if len(criteria) == 0 {
		criteria = json.RawMessage(`{}`)
	}

	row, err := q.CreateActivityServiceRule(ctx, db.CreateActivityServiceRuleParams{
		TenantID:            tenantID,
		ServiceDefinitionID: input.ServiceDefinitionID,
		ContractID:          nullableUUID(input.ContractID),
		ActivityLabel:       input.ActivityLabel,
		LegalArea:           input.LegalArea,
		Criteria:            criteria,
		ValidFrom:           input.ValidFrom,
		ValidUntil:          nullableTime(input.ValidUntil),
	})
	if isUniqueViolation(err) {
		return Rule{}, ErrConflict
	}
	if err != nil {
		return Rule{}, fmt.Errorf("create activity service rule: %w", err)
	}
	return toRule(row), nil
}

func (r *Repository) ListRules(ctx context.Context, q *db.Queries, tenantID uuid.UUID) ([]Rule, error) {
	rows, err := q.ListActivityServiceRules(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list activity service rules: %w", err)
	}
	rules := make([]Rule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, toRule(row))
	}
	return rules, nil
}

func toCatalog(row db.ServiceCatalog) Catalog {
	return Catalog{
		ID:         row.ID,
		Code:       row.Code,
		Name:       row.Name,
		Region:     row.Region,
		SourceNote: row.SourceNote,
		ValidFrom:  row.ValidFrom,
		ValidUntil: timePtr(row.ValidUntil),
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
}

func toDefinition(row db.ServiceDefinition) Definition {
	return Definition{
		ID:          row.ID,
		CatalogID:   row.CatalogID,
		Code:        row.Code,
		Name:        row.Name,
		Description: row.Description,
		LegalArea:   row.LegalArea,
		Unit:        row.Unit,
		ValidFrom:   row.ValidFrom,
		ValidUntil:  timePtr(row.ValidUntil),
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

func toContract(row db.TenantServiceContract) Contract {
	return Contract{
		ID:                  row.ID,
		TenantID:            row.TenantID,
		ServiceDefinitionID: row.ServiceDefinitionID,
		PayerName:           row.PayerName,
		ContractReference:   row.ContractReference,
		LegalArea:           row.LegalArea,
		ValidFrom:           row.ValidFrom,
		ValidUntil:          timePtr(row.ValidUntil),
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
}

func toRule(row db.ActivityServiceRule) Rule {
	return Rule{
		ID:                  row.ID,
		TenantID:            row.TenantID,
		ServiceDefinitionID: row.ServiceDefinitionID,
		ContractID:          uuidPtr(row.ContractID),
		ActivityLabel:       row.ActivityLabel,
		LegalArea:           row.LegalArea,
		Criteria:            row.Criteria,
		ValidFrom:           row.ValidFrom,
		ValidUntil:          timePtr(row.ValidUntil),
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
}

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func uuidPtr(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := value.UUID
	return &id
}

func nullableTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}

func timePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// normalizeQuery trims a search term and collapses internal whitespace so the
// ILIKE pattern stays predictable.
func normalizeQuery(query string) string {
	return strings.Join(strings.Fields(query), " ")
}
