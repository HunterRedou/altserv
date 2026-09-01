package main 

import (
	"net/http"
	"encoding/json"
	"time"
	
	"github.com/google/uuid"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/auth"
	"github.com/HunterRedou/altserv/internal/vies"
)

type Firm struct{
	ID		uuid.UUID `json:"id"`
	CreatedAt		time.Time `json:"created_at"`
	UpdatedAt		time.Time `json:"updated_at"`
	Email		string `json:"email"`
	UstId		string `json:"ustId"`
	StreetName		string `json:"streetName"`
	Plz		string `json:"plz"`
	Password		string `json:"-"`
}

func (cfg *apiConfig) handlerFirm(w http.ResponseWriter, r *http.Request)  {
	type parameters struct{
		Email string `json:"email"`
		UstId string `json:"ustId"`
		StreetName string `json:"streetName"`
		Plz string `json:"plz"`
		Password string `json:"password"`
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

	hashedPassword, err := auth.HashPassword(params.Password)
	if err != nil{
		respondWithError(w, http.StatusInternalServerError, "Couldn't hash Password", err)
		return
	}

	valid, err := vies.CheckWithRetry(r.Context(), params.UstId, 4, 3*time.Second)
	if err != nil{
		respondWithError(w, http.StatusBadGateway, "Couldn't verify UstId", err)
		return
	}
	if !valid {
		respondWithError(w, http.StatusBadRequest, "UstId is not valid", nil)
		return
	}


	firm, err := cfg.db.CreateFirm(r.Context(), db.CreateFirmParams{
		Email: params.Email,
		Ustid: params.UstId,
		Streetname: params.StreetName,
		Plz: params.Plz,
		HashedPassword: hashedPassword,
	})
	if err != nil{
		respondWithError(w, http.StatusInternalServerError, "Couldn't create a Firm", err)
		return
	}

	respondWithJSON(w, http.StatusCreated, response{
		Firm: Firm{
			ID:	firm.ID,
			CreatedAt:	firm.CreatedAt,
			UpdatedAt:	firm.UpdatedAt,
			Email:	firm.Email,
			UstId:	firm.Ustid,
			StreetName:	firm.Streetname,
			Plz:	firm.Plz,
		},
	})
}

