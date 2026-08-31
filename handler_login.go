package main

import (
	"encoding/json"
	"net/http"

	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/vies"
)

func (cfg *apiConfig) handlerLogin(w http.ResponseWriter, r *http.Request){
	type parameters struct{
		Password string `json:"password"`
		Email string `json:"email"`
		UstId string `json:"ustid"`
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

	valid, err := vies.IsValidUST(r.Context(), params.UstId)
	if err != nil{
		respondWithError(w, http.StatusBadGateway, "Couldn't verify UstId", err)
		return
	}
	if !valid {
		respondWithError(w, http.StatusBadRequest, "UstId is not valid", nil)
		return
	}

	respondWithJSON(w, http.StatusOK, response{
		User: User{
			ID:	user.ID,
			Email:	user.Email,
			CreatedAt:	user.CreatedAt,
			UpdatedAt:	user.UpdatedAt,
		},
	})

}
