package main 

import "net/http"

func (cfg *apiConfig) handlerReset(w http.ResponseWriter, r *http.Request) {
	
	if cfg.platform != "dev"{
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("401 Your are not Authorized to do that Action"))
		return
	}

		cfg.fileserverHits.Store(0)
		err := cfg.db.Reset(r.Context())
		if err != nil{
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Failed to reset Database: " + err.Error()))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hits reset to 0"))
}
