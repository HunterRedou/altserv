package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/db"
)

var errResetTokenInvalid = errors.New("password reset token is invalid")

// handlerPasswordResetRequest is public. It never reveals whether an account
// exists. In development it returns the token so the flow can be tested without
// an email sender. Production delivery is deliberately not configured yet.
func (cfg *apiConfig) handlerPasswordResetRequest(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Email string `json:"email"`
	}
	params := parameters{}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}
	if params.Email == "" {
		respondWithError(w, http.StatusBadRequest, "Missing email", nil)
		return
	}

	type response struct {
		Status    string     `json:"status"`
		Token     string     `json:"token,omitempty"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
	}
	out := response{Status: "if the account exists, a reset token was issued"}

	user, err := cfg.db.GetByEmail(r.Context(), params.Email)
	if err == nil && user.Status == "valid" && !user.DisabledAt.Valid {
		token, expiresAt, issueErr := cfg.issuePasswordResetToken(
			r.Context(),
			user.TenantID,
			user.ID,
			nil,
			"auth.password_reset_requested",
			requestIDFromContext(r.Context()),
		)
		if issueErr == nil && cfg.platform == "dev" {
			out.Token = token
			out.ExpiresAt = &expiresAt
		}
	}

	respondWithJSON(w, http.StatusAccepted, out)
}

// handlerPasswordResetConfirm is public. It consumes the token, changes the
// password, revokes every existing session and audits the change in one
// transaction.
func (cfg *apiConfig) handlerPasswordResetConfirm(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	params := parameters{}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}
	if params.Token == "" || params.Password == "" {
		respondWithError(w, http.StatusBadRequest, "Missing token or password", nil)
		return
	}

	tokenHash := auth.HashToken(params.Token)
	token, err := cfg.db.GetPasswordResetToken(r.Context(), tokenHash)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Invalid or expired reset token", nil)
		return
	}
	switch {
	case token.UsedAt.Valid,
		!time.Now().Before(token.ExpiresAt),
		token.DisabledAt.Valid,
		token.TenantStatus != "valid":
		respondWithError(w, http.StatusUnauthorized, "Invalid or expired reset token", nil)
		return
	}

	hashedPassword, err := auth.HashPassword(params.Password)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't hash Password", err)
		return
	}

	err = cfg.withTenant(r.Context(), token.TenantID, func(q *db.Queries) error {
		if _, err := q.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{
			ID:             token.UserID,
			TenantID:       token.TenantID,
			HashedPassword: hashedPassword,
		}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errUserNotFound
			}
			return err
		}
		if _, err := q.ConsumePasswordResetToken(r.Context(), tokenHash); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errResetTokenInvalid
			}
			return err
		}
		if err := q.RevokeAllUserSessions(r.Context(), db.RevokeAllUserSessionsParams{
			UserID:   token.UserID,
			TenantID: token.TenantID,
		}); err != nil {
			return err
		}
		return cfg.recordAudit(r.Context(), q, audit.Event{
			TenantID:    token.TenantID,
			ActorUserID: token.UserID,
			Action:      "auth.password_reset_completed",
			EntityType:  "user",
			EntityID:    token.UserID,
			RequestID:   requestIDFromContext(r.Context()),
		})
	})
	switch {
	case errors.Is(err, errResetTokenInvalid):
		respondWithError(w, http.StatusUnauthorized, "Invalid or expired reset token", nil)
		return
	case errors.Is(err, errUserNotFound):
		respondWithError(w, http.StatusNotFound, "User not found", nil)
		return
	case err != nil:
		respondWithError(w, http.StatusInternalServerError, "Couldn't reset the password", err)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"status": "password reset"})
}
