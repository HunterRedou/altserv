package main 

import (
	"net/http"
	"encoding/json"
	"time"
	
	"github.com/google/uuid"
	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/evatr"
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
		Email string `json:"email"`
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

	vatResp, err := cfg.evatr.CheckUst(
		r.Context(),
		evatr.ApiReq{
			RequestingVATID: cfg.reqVATID,
			RequestedVATID: params.UstId,
		},
	)
	if err != nil{
		respondWithError(w, http.StatusBadGateway, "Couldn't verify UstId", err)
		return
	}

	if vatResp.Status != "evatr-0000"{
		respondWithError(w, http.StatusBadRequest, "UstId is not Valid", nil)
		return
	}

	firm, err := cfg.db.CreateFirm(r.Context(), db.CreateFirmParams{
		Email: params.Email,
		Ustid: params.UstId,
		Streetname: params.StreetName,
		Plz: params.Plz,
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

