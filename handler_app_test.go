package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/HunterRedou/altserv/internal/observability"
)

func newAppTestRouter() http.Handler {
	cfg := &apiConfig{metrics: observability.NewRegistry(), health: observability.NewHealth(nil)}
	return cfg.routes()
}

func TestAppServesEmbeddedStartPage(t *testing.T) {
	handler := newAppTestRouter()
	want, err := os.ReadFile("start.html")
	if err != nil {
		t.Fatalf("read start.html: %v", err)
	}
	for _, path := range []string{"/app/", "/app/start.html"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
		if rec.Body.String() != string(want) {
			t.Fatalf("%s body is not start.html", path)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%s content type = %q", path, ct)
		}
	}
}

// Regression: /app/ was http.FileServer(http.Dir(".")). It served .env with
// the encryption keys, the source code and a directory listing without login.
func TestAppDoesNotServeFilesFromDisk(t *testing.T) {
	handler := newAppTestRouter()

	// Every one of these exists in the working directory during tests, so a
	// file server rooted there would answer 200.
	paths := []string{
		"/app/main.go",
		"/app/go.mod",
		"/app/routes.go",
		"/app/handler_app.go",
		"/app/.gitignore",
		"/app/docs/overview.md",
		"/app/docs/",
		"/app/internal/",
		"/app/.env",
		"/app/connetion.txt",
		"/app/.git/config",
		"/app/../go.mod",
		"/app/%2e%2e/go.mod",
		"/app/./main.go",
		"/app//main.go",
		"/app/start.html/",
		"/app/START.HTML",
	}
	for _, path := range paths {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/app/", nil)
		req.URL.Path = path
		req.URL.RawPath = ""
		handler.ServeHTTP(rec, req)
		// ServeMux redirects unclean paths (for example /app/../go.mod to
		// /go.mod). Follow the redirect once and check the target instead.
		if rec.Code >= 300 && rec.Code < 400 {
			location := rec.Header().Get("Location")
			rec = httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, location, nil))
			path += " -> " + location
		}
		if rec.Code == http.StatusOK {
			t.Fatalf("%s status = 200, want no file served", path)
		}
		body := rec.Body.String()
		for _, leak := range []string{"package main", "module github.com", "PATIENT_ENCRYPTION_KEY", "DB_URL", "<a href="} {
			if strings.Contains(body, leak) {
				t.Fatalf("%s (status %d) leaks %q: %s", path, rec.Code, leak, body)
			}
		}
	}
}

func TestRootDoesNotServeFilesFromDisk(t *testing.T) {
	handler := newAppTestRouter()
	for _, path := range []string{"/go.mod", "/main.go", "/.env", "/connetion.txt", "/start.html", "/"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, rec.Code)
		}
	}
}

func TestAppRejectsWriteMethods(t *testing.T) {
	handler := newAppTestRouter()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, "/app/", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want 405", method, rec.Code)
		}
	}
}
