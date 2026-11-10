package main

import (
	"net/http"

	"github.com/HunterRedou/altserv/internal/server"
)

// Thin wrappers keep the handlers readable while the implementation lives in
// internal/server.
func respondWithError(w http.ResponseWriter, code int, msg string, err error) {
	server.RespondWithError(w, code, msg, err)
}

func respondWithJSON(w http.ResponseWriter, code int, payload any) {
	server.RespondWithJSON(w, code, payload)
}
