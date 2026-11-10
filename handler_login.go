package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/db"
)

func (cfg *apiConfig) handlerLogin(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}

	type response struct {
		User
		CSRFToken string `json:"csrf_token"`
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	user, err := cfg.db.GetByEmail(r.Context(), params.Email)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Incorrect Email", nil)
		return
	}

	match, err := auth.CheckHash(params.Password, user.HashedPassword)
	if err != nil || !match {
		respondWithError(w, http.StatusUnauthorized, "Password not matching", nil)
		return
	}

	// Users of an unverified (or deleted) tenant must not log in.
	if user.Status != "valid" {
		respondWithError(w, http.StatusForbidden, "Tenant is not verified", nil)
		return
	}

	token, err := auth.GenerateToken()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create session", err)
		return
	}

	csrfToken, err := auth.GenerateToken()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create session", err)
		return
	}

	expiresAt := time.Now().Add(cfg.sessionTTL())
	err = cfg.withTenant(r.Context(), user.TenantID, func(q *db.Queries) error {
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
			Action:      "auth.login",
			EntityType:  "user",
			EntityID:    user.ID,
			RequestID:   requestIDFromContext(r.Context()),
		})
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create session", err)
		return
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

	respondWithJSON(w, http.StatusOK, response{
		User: User{
			ID:        user.ID,
			Email:     user.Email,
			Role:      user.Role,
			TenantID:  user.TenantID,
			CreatedAt: user.CreatedAt,
			UpdatedAt: user.UpdatedAt,
		},
		CSRFToken: csrfToken,
	})
}
