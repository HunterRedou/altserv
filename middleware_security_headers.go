package main

import (
	"net/http"
	"strings"
)

// middlewareSecurityHeaders sets conservative defaults for every response.
// API responses must not be cached because they may contain patient data.
func (cfg *apiConfig) middlewareSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		header.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			header.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
