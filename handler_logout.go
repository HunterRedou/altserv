package main

import (
	"net/http"

	"github.com/HunterRedou/altserv/internal/audit"
	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/db"
)

func (cfg *apiConfig) handlerLogout(w http.ResponseWriter, r *http.Request) {
	user, ok := authUserFromContext(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		err := cfg.withTenant(r.Context(), user.TenantID, func(q *db.Queries) error {
			if err := q.RevokeSession(r.Context(), auth.HashToken(cookie.Value)); err != nil {
				return err
			}
			return cfg.recordAudit(r.Context(), q, audit.Event{
				TenantID:    user.TenantID,
				ActorUserID: user.ID,
				Action:      "auth.logout",
				EntityType:  "user",
				EntityID:    user.ID,
				RequestID:   requestIDFromContext(r.Context()),
			})
		})
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Couldn't revoke session", err)
			return
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	respondWithJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

func (cfg *apiConfig) handlerMe(w http.ResponseWriter, r *http.Request) {
	user, ok := authUserFromContext(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	respondWithJSON(w, http.StatusOK, User{
		ID:       user.ID,
		Name:     user.Name,
		Email:    user.Email,
		Role:     user.Role,
		TenantID: user.TenantID,
	})
}
