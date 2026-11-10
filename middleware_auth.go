package main

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/google/uuid"
)

const (
	// __Host- forces Secure, Path=/ and no Domain. See doc 6.
	sessionCookieName = "__Host-session"
	sessionDuration   = 24 * time.Hour
)

// sessionTTL returns the configured session lifetime, falling back to the
// default for tests and zero-value configs.
func (cfg *apiConfig) sessionTTL() time.Duration {
	if cfg.sessionDuration > 0 {
		return cfg.sessionDuration
	}
	return sessionDuration
}

type contextKey string

const authUserKey contextKey = "auth_user"

// authUser is the authenticated identity for one request.
// TenantID always comes from the session, never from a client header.
type authUser struct {
	ID        uuid.UUID
	Name      string
	Email     string
	Role      string
	TenantID  uuid.UUID
	CSRFToken string
}

// middlewareAuth resolves the session cookie into an authUser.
func (cfg *apiConfig) middlewareAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
			return
		}

		row, err := cfg.db.GetSessionUser(r.Context(), auth.HashToken(cookie.Value))
		if err != nil {
			// Expired, revoked, unknown session, or unverified tenant.
			respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
			return
		}

		user := authUser{
			ID:        row.ID,
			Name:      row.Name,
			Email:     row.Email,
			Role:      row.Role,
			TenantID:  row.TenantID,
			CSRFToken: row.CsrfToken,
		}
		ctx := context.WithValue(r.Context(), authUserKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// middlewareCSRF requires a matching X-CSRF-Token header on unsafe methods.
func (cfg *apiConfig) middlewareCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		user, ok := authUserFromContext(r.Context())
		if !ok {
			respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
			return
		}

		header := r.Header.Get("X-CSRF-Token")
		if subtle.ConstantTimeCompare([]byte(header), []byte(user.CSRFToken)) != 1 {
			respondWithError(w, http.StatusForbidden, "Invalid CSRF token", nil)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func authUserFromContext(ctx context.Context) (authUser, bool) {
	user, ok := ctx.Value(authUserKey).(authUser)
	return user, ok
}
