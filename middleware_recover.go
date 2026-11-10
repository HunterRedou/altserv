package main

import (
	"log/slog"
	"net/http"
)

// middlewareRecover turns a panic into a 500 without leaking implementation
// details to the client. The panic is logged with the request id for correlation.
func (cfg *apiConfig) middlewareRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("panic recovered",
					"request_id", requestIDFromContext(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"panic", recovered,
				)
				respondWithError(w, http.StatusInternalServerError, "Internal server error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
