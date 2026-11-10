package server

import (
	"encoding/json"
	"log"
	"net/http"
)

// RespondWithError writes a JSON error and logs the underlying error.
// The client never sees internal error details.
func RespondWithError(w http.ResponseWriter, code int, msg string, err error) {
	if err != nil {
		log.Println(err)
	}
	if code > 499 {
		log.Printf("Responding with 5XX error: %s", msg)
	}
	RespondWithJSON(w, code, ErrorResponse{Error: msg})
}

// RespondWithJSON writes a JSON response.
func RespondWithJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error marshalling JSON: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(code)
	if _, err := w.Write(data); err != nil {
		log.Printf("Error writing response: %s", err)
	}
}
