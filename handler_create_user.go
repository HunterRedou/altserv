package main 

import (
	"net/http"
	"encoding/json"
	"time"
	
	"github.com/google/uuid"
	"github.com/HunterRedou/altserv/internal/db"
)

type User struct{
	ID		uuid.UUID `json:"id"`
	CreatedAt		time.Time `json:"created_at"`
	UpdatedAt		time.Time `json:"updated_at"`
	Name		string `json:"name"`
	IsAdmin		bool `json:"is_admin"`
	IsTeamhead		bool `json:"is_teamhead"`
}

func (cfg *apiConfig) handlerUser(w http.ResponseWriter, r *http.Request)  {
	type parameters struct{
		Name string `json:"name"`
		IsAdmin bool `json:"is_admin"`
		IsTeamhead bool `json:"is_teamhead"`
	}
	type response struct{
		User
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't decode parameters", err)
		return
	}

	user, err := cfg.db.CreateUser(r.Context(), db.CreateUserParams{
		Name: params.Name,
		IsAdmin: params.IsAdmin,
		IsTeamhead: params.IsTeamhead,
	})
	if err != nil{
		respondWithError(w, http.StatusInternalServerError, "Couldn't create a User", err)
		return
	}

	respondWithJSON(w, http.StatusCreated, response{
		User: User{
			ID:	user.ID,
			CreatedAt:	user.CreatedAt,
			UpdatedAt:	user.UpdatedAt,
			Name:	user.Name,
			IsAdmin:	user.IsAdmin,
			IsTeamhead:	user.IsTeamhead,
		},
	})
}
