package main

import (
	"encoding/json"
	"net/http"

	"github.com/HunterRedou/altserv/internal/auth"
)

func (cfg *apiConfig) handlerLogin(w http.ResponseWriter, r *http.Request){
	type parameters struct{
		Password string `json:"password"`
		Email string `json:"email"`
	}
	type response struct{
		User
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err != nil{
		respondWithError(w, http.StatusInternalServerError, "Couldn't decode parameters", err)
		return
	}

	user, err := cfg.db.GetByEmail(r.Context(), params.Email)
	if err != nil{
		respondWithError(w, http.StatusUnauthorized, "Incorrect Email", err)
		return
	}

	match, err := auth.CheckHash(params.Password, user.HashedPassword)
	if err != nil || !match {
		respondWithError(w, http.StatusUnauthorized, "Password not matching", err)
		return
	}

	respondWithJSON(w, http.StatusOK, response{
		User: User{
			ID:		user.ID,
			Email:		user.Email,
			CreatedAt:		user.CreatedAt,
			UpdatedAt:		user.UpdatedAt,
		},
	})
}
