package main

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

const requestIDHeader = "X-Request-ID"

type requestIDContextKey struct{}

// middlewareRequestID accepts an upstream request id or generates one, puts it
// in the response and in the request context for audit correlation.
func (cfg *apiConfig) middlewareRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDContextKey{}, id)))
	})
}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey{}).(string)
	return id
}
