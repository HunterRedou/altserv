package main

import (
	"bytes"
	_ "embed"
	"net/http"
	"time"
)

// startPage is compiled into the binary. /app/ must never serve a directory
// from disk: a file server rooted at the working directory exposed .env with
// the encryption keys, the source code and every other file next to the binary.
//
//go:embed start.html
var startPage []byte

// startPageModTime is fixed so ServeContent can answer conditional requests
// without reading the file system.
var startPageModTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// handlerApp serves the public start page. Only an explicit allowlist of paths
// is answered; everything else under /app/ is 404, without a directory listing.
func (cfg *apiConfig) handlerApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/app/", "/app/start.html":
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "start.html", startPageModTime, bytes.NewReader(startPage))
}
