package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/google/uuid"
)

type Tenant struct {
	ID         uuid.UUID `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Email      string    `json:"email"`
	UstId      string    `json:"ustId"`
	StreetName string    `json:"streetName"`
	Plz        string    `json:"plz"`
	Status     string    `json:"status"`
	Password   string    `json:"-"`
}

func (cfg *apiConfig) handlerCreateTenant(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Email      string `json:"email"`
		UstId      string `json:"ustId"`
		StreetName string `json:"streetName"`
		Plz        string `json:"plz"`
		Password   string `json:"password"`
	}
	type response struct {
		Tenant
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't decode parameters", err)
		return
	}

	if !validUStIDFormat(params.UstId) {
		respondWithError(w, http.StatusBadRequest, "UstId is not valid", nil)
		return
	}

	hashedPassword, err := auth.HashPassword(params.Password)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't hash Password", err)
		return
	}

	tenant, err := cfg.db.CreateTenant(r.Context(), db.CreateTenantParams{
		Email:          params.Email,
		Ustid:          params.UstId,
		Streetname:     params.StreetName,
		Plz:            params.Plz,
		HashedPassword: hashedPassword,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create a Tenant", err)
		return
	}

	// The tenant exists now with status "pending". Verification happens in the
	// background and will either set it to "valid" or delete it.
	cfg.verifyTenantAsync(tenant.ID, tenant.Ustid)

	respondWithJSON(w, http.StatusCreated, response{
		Tenant: Tenant{
			ID:         tenant.ID,
			CreatedAt:  tenant.CreatedAt,
			UpdatedAt:  tenant.UpdatedAt,
			Email:      tenant.Email,
			UstId:      tenant.Ustid,
			StreetName: tenant.Streetname,
			Plz:        tenant.Plz,
			Status:     tenant.Status,
		},
	})
}
