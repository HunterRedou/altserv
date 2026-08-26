package main 

import (
	"net/http"
	"encoding/json"
	"time"
	
	"github.com/google/uuid"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/auth"
)

type User struct{
	ID		uuid.UUID `json:"id"`
	CreatedAt		time.Time `json:"created_at"`
	UpdatedAt		time.Time `json:"updated_at"`
	Name		string `json:"name"`
	IsAdmin		bool `json:"is_admin"`
	IsTeamhead		bool `json:"is_teamhead"`
	Password		string `json:"-"`
	Email		string `json:"email"`
}

func (cfg *apiConfig) handlerUser(w http.ResponseWriter, r *http.Request)  {
	type parameters struct{
		Name string `json:"name"`
		IsAdmin bool `json:"is_admin"`
		IsTeamhead bool `json:"is_teamhead"`
		Password string `json:"password"`
		Email string `json:"email"`
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

	hashedPassword, err := auth.HashPassword(params.Password)
	if err != nil{
		respondWithError(w, http.StatusInternalServerError, "Couldn't hash Password", err)
		return
	}

	user, err := cfg.db.CreateUser(r.Context(), db.CreateUserParams{
		Name: params.Name,
		IsAdmin: params.IsAdmin,
		IsTeamhead: params.IsTeamhead,
		HashedPassword: hashedPassword,
		Email: params.Email,
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

func (cfg *apiConfig) handlerGetUsers(w http.ResponseWriter, r *http.Request){
	dbUsers, err := cfg.db.GetUser(r.Context())
	if err != nil{
		respondWithError(w, http.StatusNotFound, "No Users Found", err)
		return
	}
	
	users := []User{}
	for _, dbUser := range dbUsers{
		users = append(users, User{
			ID: dbUser.ID,
			CreatedAt: dbUser.CreatedAt,
			UpdatedAt: dbUser.UpdatedAt,
			Name: dbUser.Name,
			IsAdmin: dbUser.IsAdmin,
			IsTeamhead: dbUser.IsTeamhead,
		})
	}
	respondWithJSON(w, http.StatusOK, users)
	
}
