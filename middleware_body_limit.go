package main

import "net/http"

// maxRequestBodyBytes limits JSON request bodies. Patient data is small; a
// larger body is almost certainly a mistake or an attack.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

func (cfg *apiConfig) middlewareBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}
