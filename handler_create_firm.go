package main 

import (
	"net/http"
	"encoding/json"
	"time"
	
	"github.com/google/uuid"
	"github.com/HunterRedou/altserv/internal/db"
)

type Firm struct{
	ID		uuid.UUID `json:"id"`
	CreatedAt		time.Time `json:"created_at"`
	UpdatedAt		time.Time `json:"updated_at"`
	Email		string `json:"email"`
	UstId		string `json:"ustId"`
	StreetName		string `json:"streetName"`
	Plz		string `json:"plz"`
}

func (cfg *apiConfig) handlerFirm(w http.ResponseWriter, r *http.Request)  {
	type parameters struct{
		Email string `json:"name"`
		UstId string `json:"ustId"`
		StreetName string `json:"streetName"`
		Plz string `json:"plz"`
	}
	type response struct{
		Firm
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't decode parameters", err)
		return
	}

	firm, err := cfg.db.CreateFirm(r.Context(), db.CreateUserParams{
		Email: params.Email,
		UstId: params.UstId,
		StreetName: params.StreetName,
		Plz: params.Plz,
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
			Email:	user.Email,
			UstId:	user.UstId,
			StreetName:	user.StreetName,
			Plz:	user.Plz,
		},
	})
}

