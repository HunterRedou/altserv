package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

// handlerLoginMFA is step two of login. It accepts a TOTP code or a one-time
// recovery code and creates the session only when the challenge is valid. The
// challenge is consumed in the same transaction as the session.
func (cfg *apiConfig) handlerLoginMFA(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Challenge    string `json:"challenge"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}

	params := parameters{}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}
	if params.Challenge == "" {
		respondWithError(w, http.StatusBadRequest, "Missing challenge", nil)
		return
	}

	challenge, err := cfg.db.GetMFAChallenge(r.Context(), auth.HashToken(params.Challenge))
	if err != nil {
		cfg.metrics.IncLoginFailure()
		respondWithError(w, http.StatusUnauthorized, "Invalid challenge", nil)
		return
	}

	switch {
	case challenge.Purpose != "login":
		respondWithError(w, http.StatusUnauthorized, "Invalid challenge", nil)
		return
	case challenge.ConsumedAt.Valid,
		challenge.Attempts >= maxMFAAttempts,
		!time.Now().Before(challenge.ExpiresAt),
		challenge.DisabledAt.Valid,
		challenge.TenantStatus != "valid":
		respondWithError(w, http.StatusUnauthorized, "Challenge expired", nil)
		return
	}

	user := db.User{
		ID:                  challenge.UserID,
		CreatedAt:           challenge.CreatedAt,
		UpdatedAt:           challenge.UpdatedAt,
		Name:                challenge.Name,
		Email:               challenge.Email,
		TenantID:            challenge.TenantID,
		Role:                challenge.Role,
		DisabledAt:          challenge.DisabledAt,
		MfaSecretEnc:        challenge.MfaSecretEnc,
		MfaSecretKeyVersion: challenge.MfaSecretKeyVersion,
		MfaEnabled:          challenge.MfaEnabled,
	}

	identity := loginUser{
		ID:        user.ID,
		TenantID:  user.TenantID,
		Name:      user.Name,
		Email:     user.Email,
		Role:      user.Role,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}

	err = cfg.issueSession(w, r, identity, "auth.login", false, func(ctx context.Context, q *db.Queries) error {
		if err := cfg.consumeSecondFactor(ctx, q, user, params.Code, params.RecoveryCode); err != nil {
			return err
		}
		if _, err := q.ConsumeMFAChallenge(ctx, challenge.TokenHash); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errSecondFactorInvalid
			}
			return err
		}
		return nil
	})
	switch {
	case errors.Is(err, errSecondFactorInvalid):
		cfg.recordMFAFailure(r.Context(), challenge, requestIDFromContext(r.Context()))
		respondWithError(w, http.StatusUnauthorized, "Invalid second factor", nil)
		return
	case err != nil:
		respondWithError(w, http.StatusInternalServerError, "Couldn't create session", err)
		return
	}
}

// handlerMFAEnroll starts enrollment. It returns the shared secret once. The
// secret is encrypted at rest and is never written to the audit log.
func (cfg *apiConfig) handlerMFAEnroll(w http.ResponseWriter, r *http.Request) {
	actor, ok := authUserFromContext(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	if cfg.mfaBox == nil {
		respondWithError(w, http.StatusInternalServerError, "MFA is not configured", errMFAUnavailable)
		return
	}

	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create MFA secret", err)
		return
	}
	encrypted, err := cfg.mfaBox.Encrypt(
		secret,
		auth.MFAFieldAAD(actor.TenantID, actor.ID, cfg.mfaBox.KeyVersion()),
	)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't protect MFA secret", err)
		return
	}
	keyVersion := sql.NullInt32{Int32: int32(cfg.mfaBox.KeyVersion()), Valid: true}

	err = cfg.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		current, err := q.GetUserByID(r.Context(), db.GetUserByIDParams{ID: actor.ID, TenantID: actor.TenantID})
		if errors.Is(err, sql.ErrNoRows) {
			return errUserNotFound
		}
		if err != nil {
			return err
		}
		if current.MfaEnabled {
			return errMFAAlreadyEnabled
		}
		if _, err := q.SetUserMFASecret(r.Context(), db.SetUserMFASecretParams{
			ID:                  actor.ID,
			TenantID:            actor.TenantID,
			MfaSecretEnc:        encrypted,
			MfaSecretKeyVersion: keyVersion,
		}); err != nil {
			return err
		}
		return cfg.recordAudit(r.Context(), q, audit.Event{
			TenantID:    actor.TenantID,
			ActorUserID: actor.ID,
			Action:      "auth.mfa_enrollment_started",
			EntityType:  "user",
			EntityID:    actor.ID,
			RequestID:   requestIDFromContext(r.Context()),
		})
	})
	switch {
	case errors.Is(err, errMFAAlreadyEnabled):
		respondWithError(w, http.StatusConflict, "MFA is already enabled", nil)
		return
	case errors.Is(err, errUserNotFound):
		respondWithError(w, http.StatusNotFound, "User not found", nil)
		return
	case err != nil:
		respondWithError(w, http.StatusInternalServerError, "Couldn't start MFA enrollment", err)
		return
	}

	respondWithJSON(w, http.StatusOK, struct {
		Secret string `json:"secret"`
		URI    string `json:"otpauth_uri"`
	}{
		Secret: secret,
		URI:    auth.OTPAuthURI(mfaIssuer, actor.Email, secret),
	})
}

// handlerMFAConfirm finishes enrollment after the user proves one code. It
// returns the recovery codes once; only their hashes are stored.
func (cfg *apiConfig) handlerMFAConfirm(w http.ResponseWriter, r *http.Request) {
	actor, ok := authUserFromContext(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	if cfg.mfaBox == nil {
		respondWithError(w, http.StatusInternalServerError, "MFA is not configured", errMFAUnavailable)
		return
	}

	type parameters struct {
		Code string `json:"code"`
	}
	params := parameters{}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	codes, err := auth.GenerateRecoveryCodes()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create recovery codes", err)
		return
	}

	err = cfg.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		current, err := q.GetUserByID(r.Context(), db.GetUserByIDParams{ID: actor.ID, TenantID: actor.TenantID})
		if errors.Is(err, sql.ErrNoRows) {
			return errUserNotFound
		}
		if err != nil {
			return err
		}
		if current.MfaEnabled {
			return errMFAAlreadyEnabled
		}
		secret, err := cfg.decryptMFASecret(current)
		if err != nil {
			return err
		}
		if !auth.ValidateTOTP(secret, params.Code, time.Now()) {
			return errSecondFactorInvalid
		}

		if err := q.DeleteRecoveryCodes(r.Context(), db.DeleteRecoveryCodesParams{
			TenantID: actor.TenantID,
			UserID:   actor.ID,
		}); err != nil {
			return err
		}
		for _, code := range codes {
			if err := q.CreateRecoveryCode(r.Context(), db.CreateRecoveryCodeParams{
				TenantID: actor.TenantID,
				UserID:   actor.ID,
				CodeHash: auth.HashRecoveryCode(code),
			}); err != nil {
				return err
			}
		}
		if _, err := q.EnableUserMFA(r.Context(), db.EnableUserMFAParams{
			ID:                  actor.ID,
			TenantID:            actor.TenantID,
			MfaSecretEnc:        current.MfaSecretEnc,
			MfaSecretKeyVersion: current.MfaSecretKeyVersion,
		}); err != nil {
			return err
		}
		return cfg.recordAudit(r.Context(), q, audit.Event{
			TenantID:    actor.TenantID,
			ActorUserID: actor.ID,
			Action:      "auth.mfa_enabled",
			EntityType:  "user",
			EntityID:    actor.ID,
			RequestID:   requestIDFromContext(r.Context()),
		})
	})
	switch {
	case errors.Is(err, errSecondFactorInvalid):
		respondWithError(w, http.StatusBadRequest, "Invalid code", nil)
		return
	case errors.Is(err, errMFAAlreadyEnabled):
		respondWithError(w, http.StatusConflict, "MFA is already enabled", nil)
		return
	case errors.Is(err, errUserNotFound):
		respondWithError(w, http.StatusNotFound, "User not found", nil)
		return
	case err != nil:
		respondWithError(w, http.StatusInternalServerError, "Couldn't confirm MFA enrollment", err)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// handlerMFADisable removes MFA after the user proves both the password and a
// current second factor. Removing a second factor is audited.
func (cfg *apiConfig) handlerMFADisable(w http.ResponseWriter, r *http.Request) {
	actor, ok := authUserFromContext(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	type parameters struct {
		Password     string `json:"password"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	params := parameters{}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	err := cfg.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		current, err := q.GetUserByID(r.Context(), db.GetUserByIDParams{ID: actor.ID, TenantID: actor.TenantID})
		if errors.Is(err, sql.ErrNoRows) {
			return errUserNotFound
		}
		if err != nil {
			return err
		}
		if !current.MfaEnabled {
			return errMFANotEnabled
		}
		match, err := auth.CheckHash(params.Password, current.HashedPassword)
		if err != nil || !match {
			return errSecondFactorInvalid
		}
		if err := cfg.consumeSecondFactor(r.Context(), q, current, params.Code, params.RecoveryCode); err != nil {
			return err
		}
		if _, err := q.DisableUserMFA(r.Context(), db.DisableUserMFAParams{ID: actor.ID, TenantID: actor.TenantID}); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodes(r.Context(), db.DeleteRecoveryCodesParams{
			TenantID: actor.TenantID,
			UserID:   actor.ID,
		}); err != nil {
			return err
		}
		return cfg.recordAudit(r.Context(), q, audit.Event{
			TenantID:    actor.TenantID,
			ActorUserID: actor.ID,
			Action:      "auth.mfa_disabled",
			EntityType:  "user",
			EntityID:    actor.ID,
			RequestID:   requestIDFromContext(r.Context()),
		})
	})
	switch {
	case errors.Is(err, errSecondFactorInvalid):
		respondWithError(w, http.StatusUnauthorized, "Invalid password or second factor", nil)
		return
	case errors.Is(err, errMFANotEnabled):
		respondWithError(w, http.StatusConflict, "MFA is not enabled", nil)
		return
	case err != nil:
		respondWithError(w, http.StatusInternalServerError, "Couldn't disable MFA", err)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"status": "mfa disabled"})
}

// handlerMFARecoveryCodes replaces the recovery codes after the user proves the
// password and a current second factor.
func (cfg *apiConfig) handlerMFARecoveryCodes(w http.ResponseWriter, r *http.Request) {
	actor, ok := authUserFromContext(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	type parameters struct {
		Password     string `json:"password"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	params := parameters{}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	codes, err := auth.GenerateRecoveryCodes()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create recovery codes", err)
		return
	}

	err = cfg.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		current, err := q.GetUserByID(r.Context(), db.GetUserByIDParams{ID: actor.ID, TenantID: actor.TenantID})
		if errors.Is(err, sql.ErrNoRows) {
			return errUserNotFound
		}
		if err != nil {
			return err
		}
		if !current.MfaEnabled {
			return errMFANotEnabled
		}
		match, err := auth.CheckHash(params.Password, current.HashedPassword)
		if err != nil || !match {
			return errSecondFactorInvalid
		}
		if err := cfg.consumeSecondFactor(r.Context(), q, current, params.Code, params.RecoveryCode); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodes(r.Context(), db.DeleteRecoveryCodesParams{
			TenantID: actor.TenantID,
			UserID:   actor.ID,
		}); err != nil {
			return err
		}
		for _, code := range codes {
			if err := q.CreateRecoveryCode(r.Context(), db.CreateRecoveryCodeParams{
				TenantID: actor.TenantID,
				UserID:   actor.ID,
				CodeHash: auth.HashRecoveryCode(code),
			}); err != nil {
				return err
			}
		}
		return cfg.recordAudit(r.Context(), q, audit.Event{
			TenantID:    actor.TenantID,
			ActorUserID: actor.ID,
			Action:      "auth.recovery_codes_regenerated",
			EntityType:  "user",
			EntityID:    actor.ID,
			RequestID:   requestIDFromContext(r.Context()),
		})
	})
	switch {
	case errors.Is(err, errSecondFactorInvalid):
		respondWithError(w, http.StatusUnauthorized, "Invalid password or second factor", nil)
		return
	case errors.Is(err, errMFANotEnabled):
		respondWithError(w, http.StatusConflict, "MFA is not enabled", nil)
		return
	case err != nil:
		respondWithError(w, http.StatusInternalServerError, "Couldn't replace recovery codes", err)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// handlerAdminPasswordReset lets a chef or platform admin issue a single-use
// reset token for another user in the same tenant. There is no email delivery,
// so the token is returned to the issuer and must be handed over out of band.
func (cfg *apiConfig) handlerAdminPasswordReset(w http.ResponseWriter, r *http.Request) {
	actor, ok := authUserFromContext(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}
	if !canManageUsers(actor.Role) {
		respondWithError(w, http.StatusForbidden, "Not allowed to reset passwords", nil)
		return
	}

	targetID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid user id", nil)
		return
	}
	if targetID == actor.ID {
		respondWithError(w, http.StatusBadRequest, "Use the self-service reset for your own account", nil)
		return
	}

	var target db.User
	err = cfg.withTenant(r.Context(), actor.TenantID, func(q *db.Queries) error {
		var err error
		target, err = q.GetUserByID(r.Context(), db.GetUserByIDParams{ID: targetID, TenantID: actor.TenantID})
		return err
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		respondWithError(w, http.StatusNotFound, "User not found", nil)
		return
	case err != nil:
		respondWithError(w, http.StatusInternalServerError, "Couldn't read user", err)
		return
	}

	if actor.Role == "chef" && target.Role == "platform_admin" {
		respondWithError(w, http.StatusForbidden, "Not allowed to reset this account", nil)
		return
	}
	if target.DisabledAt.Valid {
		respondWithError(w, http.StatusConflict, "Account is disabled", nil)
		return
	}

	token, expiresAt, err := cfg.issuePasswordResetToken(
		r.Context(),
		target.TenantID,
		target.ID,
		&actor.ID,
		"auth.password_reset_issued",
		requestIDFromContext(r.Context()),
	)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't issue reset token", err)
		return
	}

	respondWithJSON(w, http.StatusCreated, struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}{Token: token, ExpiresAt: expiresAt})
}
