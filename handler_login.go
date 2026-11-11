package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/HunterRedou/altserv/internal/auth"
)

// handlerLogin is step one of authentication. A privileged user who has
// enrolled MFA receives a short-lived challenge instead of a session. A
// privileged user who has not enrolled yet receives a restricted session that
// can only reach the enrollment endpoints (doc 4, ADR-020).
func (cfg *apiConfig) handlerLogin(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Password string `json:"password"`
		Email    string `json:"email"`
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
		cfg.metrics.IncLoginFailure()
		respondWithError(w, http.StatusUnauthorized, "Incorrect Email", nil)
		return
	}

	match, err := auth.CheckHash(params.Password, user.HashedPassword)
	if err != nil || !match {
		cfg.metrics.IncLoginFailure()
		respondWithError(w, http.StatusUnauthorized, "Password not matching", nil)
		return
	}

	if user.DisabledAt.Valid {
		respondWithError(w, http.StatusForbidden, "Account is disabled", nil)
		return
	}

	// Users of an unverified (or deleted) tenant must not log in.
	if user.Status != "valid" {
		respondWithError(w, http.StatusForbidden, "Tenant is not verified", nil)
		return
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

	// A privileged account with MFA enabled must present the second factor
	// before a session is created. No cookie is set on this response.
	if mfaRequired(user.Role) && user.MfaEnabled {
		challenge, err := cfg.newMFAChallenge(r.Context(), user.TenantID, user.ID, "login")
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Couldn't start MFA challenge", err)
			return
		}
		respondWithJSON(w, http.StatusAccepted, struct {
			MFARequired bool      `json:"mfa_required"`
			Challenge   string    `json:"challenge"`
			ExpiresAt   time.Time `json:"expires_at"`
		}{
			MFARequired: true,
			Challenge:   challenge,
			ExpiresAt:   time.Now().Add(mfaChallengeTTL),
		})
		return
	}

	// A privileged account without MFA gets a restricted session so it can
	// enroll. middlewareAuth blocks every other route until enrollment ends.
	enrollmentRequired := mfaRequired(user.Role) && !user.MfaEnabled
	if err := cfg.issueSession(w, r, identity, "auth.login", enrollmentRequired, nil); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create session", err)
	}
}
