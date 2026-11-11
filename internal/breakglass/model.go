package breakglass

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid break-glass input")
	ErrNotFound  = errors.New("break-glass grant not found")
	ErrConflict  = errors.New("break-glass grant is not in a valid state")
)

const (
	// DefaultDurationMinutes is the default access window.
	DefaultDurationMinutes = 30
	// MinDurationMinutes bounds an approval so a typo cannot grant a long window.
	MinDurationMinutes = 1
	// MaxDurationMinutes is the hard ceiling for one approval.
	MaxDurationMinutes = 60
	maxReasonRunes     = 1000
)

// Actor is the authenticated user performing the operation.
type Actor struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	Role     string
}

// Grant is one break-glass request and its decision. Status is derived as
// "expired" when an approved grant is past its expiry, even though the stored
// row still says "approved".
type Grant struct {
	ID             uuid.UUID  `json:"id"`
	TenantID       uuid.UUID  `json:"tenant_id"`
	PatientID      uuid.UUID  `json:"patient_id"`
	RequestedBy    uuid.UUID  `json:"requested_by"`
	DecidedBy      *uuid.UUID `json:"decided_by,omitempty"`
	Status         string     `json:"status"`
	Reason         string     `json:"reason"`
	DecisionReason string     `json:"decision_reason,omitempty"`
	RequestedAt    time.Time  `json:"requested_at"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
}

// RequestInput is the validated input for a new request.
type RequestInput struct {
	PatientID uuid.UUID
	Reason    string
}

// DecisionInput is the input for an approval or denial.
type DecisionInput struct {
	DurationMinutes int
	DecisionReason  string
}

// RevokeInput is the input for a revocation. TenantID is only used for a
// platform_admin, who must name the target tenant.
type RevokeInput struct {
	TenantID uuid.UUID
	Reason   string
}

// withEffectiveStatus reports an approved grant as expired once the stored
// expiry has passed. The active-grant query uses clock_timestamp() in SQL; this
// helper is only for display.
func (g Grant) withEffectiveStatus(now time.Time) Grant {
	if g.Status == "approved" && g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
		g.Status = "expired"
	}
	return g
}
