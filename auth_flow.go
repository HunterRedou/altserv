package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

const (
	// mfaChallengeTTL is short because the user already proved the password.
	mfaChallengeTTL = 5 * time.Minute
	// maxMFAAttempts invalidates a challenge before a code can be brute-forced.
	maxMFAAttempts = 5
	// passwordResetTTL bounds how long an issued reset token is useful.
	passwordResetTTL = 30 * time.Minute
	// mfaIssuer is the label shown by an authenticator app.
	mfaIssuer = "altserv"
)

var (
	errSecondFactorInvalid = errors.New("invalid second factor")
	errMFAUnavailable      = errors.New("mfa is not configured")
	errMFAAlreadyEnabled   = errors.New("mfa is already enabled")
	errMFANotEnabled       = errors.New("mfa is not enabled")
)

// loginUser is the small identity subset needed to issue a session.
type loginUser struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Name      string
	Email     string
	Role      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// mfaRequired reports whether a role must use a second factor. The platform
// admin is included because it can create tenant administrators.
func mfaRequired(role string) bool {
	return role == "chef" || role == "platform_admin"
}

// mfaAllowedWithoutEnrollment is the narrow set of routes a privileged user may
// reach with a legacy session before MFA enrollment is complete.
func mfaAllowedWithoutEnrollment(path string) bool {
	switch path {
	case "/api/me", "/api/logout", "/api/mfa/enroll", "/api/mfa/confirm":
		return true
	}
	return false
}

// issueSession creates a session, sets the cookie and writes the response.
// before runs in the same transaction, so a challenge or recovery code is
// consumed only if the session is created.
func (cfg *apiConfig) issueSession(
	w http.ResponseWriter,
	r *http.Request,
	user loginUser,
	action string,
	enrollmentRequired bool,
	before func(context.Context, *db.Queries) error,
) error {
	token, err := auth.GenerateToken()
	if err != nil {
		return err
	}
	csrfToken, err := auth.GenerateToken()
	if err != nil {
		return err
	}

	expiresAt := time.Now().Add(cfg.sessionTTL())
	err = cfg.withTenant(r.Context(), user.TenantID, func(q *db.Queries) error {
		if before != nil {
			if err := before(r.Context(), q); err != nil {
				return err
			}
		}
		if _, err := q.CreateSession(r.Context(), db.CreateSessionParams{
			TokenHash: auth.HashToken(token),
			UserID:    user.ID,
			TenantID:  user.TenantID,
			CsrfToken: csrfToken,
			ExpiresAt: expiresAt,
		}); err != nil {
			return err
		}
		return cfg.recordAudit(r.Context(), q, audit.Event{
			TenantID:    user.TenantID,
			ActorUserID: user.ID,
			Action:      action,
			EntityType:  "user",
			EntityID:    user.ID,
			RequestID:   requestIDFromContext(r.Context()),
		})
	})
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})

	respondWithJSON(w, http.StatusOK, struct {
		User
		CSRFToken             string `json:"csrf_token"`
		MFAEnrollmentRequired bool   `json:"mfa_enrollment_required,omitempty"`
	}{
		User: User{
			ID:        user.ID,
			CreatedAt: user.CreatedAt,
			UpdatedAt: user.UpdatedAt,
			Name:      user.Name,
			Role:      user.Role,
			Email:     user.Email,
			TenantID:  user.TenantID,
		},
		CSRFToken:             csrfToken,
		MFAEnrollmentRequired: enrollmentRequired,
	})
	return nil
}

// newMFAChallenge creates a single-use second-step challenge. The plaintext
// token is returned to the client and only its hash is stored.
func (cfg *apiConfig) newMFAChallenge(ctx context.Context, tenantID, userID uuid.UUID, purpose string) (string, error) {
	token, err := auth.GenerateToken()
	if err != nil {
		return "", err
	}
	err = cfg.withTenant(ctx, tenantID, func(q *db.Queries) error {
		_, err := q.CreateMFAChallenge(ctx, db.CreateMFAChallengeParams{
			TokenHash: auth.HashToken(token),
			TenantID:  tenantID,
			UserID:    userID,
			Purpose:   purpose,
			ExpiresAt: time.Now().Add(mfaChallengeTTL),
		})
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// decryptMFASecret opens the stored TOTP seed for one user.
func (cfg *apiConfig) decryptMFASecret(user db.User) (string, error) {
	if cfg.mfaBox == nil {
		return "", errMFAUnavailable
	}
	if len(user.MfaSecretEnc) == 0 || !user.MfaSecretKeyVersion.Valid {
		return "", errMFAUnavailable
	}
	return cfg.mfaBox.Decrypt(
		user.MfaSecretEnc,
		auth.MFAFieldAAD(user.TenantID, user.ID, int(user.MfaSecretKeyVersion.Int32)),
	)
}

// consumeSecondFactor accepts either a TOTP code or a one-time recovery code.
// The caller must run it inside the transaction that performs the protected
// change, so a failed operation does not burn a recovery code.
func (cfg *apiConfig) consumeSecondFactor(ctx context.Context, q *db.Queries, user db.User, code, recoveryCode string) error {
	if code != "" {
		if secret, err := cfg.decryptMFASecret(user); err == nil {
			if auth.ValidateTOTP(secret, code, time.Now()) {
				return nil
			}
		}
	}
	if recoveryCode != "" {
		_, err := q.ConsumeRecoveryCode(ctx, db.ConsumeRecoveryCodeParams{
			TenantID: user.TenantID,
			UserID:   user.ID,
			CodeHash: auth.HashRecoveryCode(recoveryCode),
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return errSecondFactorInvalid
}

// recordMFAFailure increments the challenge counter and audits the failure.
// It is best effort: a failed audit must not turn a 401 into a 500.
func (cfg *apiConfig) recordMFAFailure(ctx context.Context, challenge db.GetMFAChallengeRow, requestID string) {
	cfg.metrics.IncLoginFailure()
	_ = cfg.withTenant(ctx, challenge.TenantID, func(q *db.Queries) error {
		attempts, err := q.IncrementMFAChallengeAttempts(ctx, challenge.TokenHash)
		if err != nil {
			return err
		}
		if attempts >= maxMFAAttempts {
			_, _ = q.ConsumeMFAChallenge(ctx, challenge.TokenHash)
		}
		return cfg.recordAudit(ctx, q, audit.Event{
			TenantID:    challenge.TenantID,
			ActorUserID: challenge.UserID,
			Action:      "auth.mfa_failed",
			EntityType:  "user",
			EntityID:    challenge.UserID,
			RequestID:   requestID,
			Metadata:    map[string]any{"purpose": challenge.Purpose},
		})
	})
}

// issuePasswordResetToken creates a single-use reset token and audits it.
// issuedBy is nil for a self-service request.
func (cfg *apiConfig) issuePasswordResetToken(
	ctx context.Context,
	tenantID, userID uuid.UUID,
	issuedBy *uuid.UUID,
	action string,
	requestID string,
) (string, time.Time, error) {
	token, err := auth.GenerateToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().Add(passwordResetTTL)

	issuer := uuid.NullUUID{}
	if issuedBy != nil {
		issuer = uuid.NullUUID{UUID: *issuedBy, Valid: true}
	}

	err = cfg.withTenant(ctx, tenantID, func(q *db.Queries) error {
		if err := q.DeleteUnusedPasswordResetTokens(ctx, db.DeleteUnusedPasswordResetTokensParams{
			TenantID: tenantID,
			UserID:   userID,
		}); err != nil {
			return err
		}
		if _, err := q.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
			TokenHash: auth.HashToken(token),
			TenantID:  tenantID,
			UserID:    userID,
			ExpiresAt: expiresAt,
			IssuedBy:  issuer,
		}); err != nil {
			return err
		}
		return cfg.recordAudit(ctx, q, audit.Event{
			TenantID:    tenantID,
			ActorUserID: uuidOrNil(issuedBy),
			Action:      action,
			EntityType:  "user",
			EntityID:    userID,
			RequestID:   requestID,
			Metadata:    map[string]any{"self_service": issuedBy == nil},
		})
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func uuidOrNil(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}
