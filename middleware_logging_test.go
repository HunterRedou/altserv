package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLoggingDoesNotLogRequestBody proves that the request logger never writes
// the body. Bodies can contain patient names, free-text reasons and health
// data, so they must not reach the logs even on an error path (doc 15/17).
func TestLoggingDoesNotLogRequestBody(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(previous)

	cfg := &apiConfig{}
	handler := cfg.middlewareLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Consume the body the way a real handler would, then return an error
		// status so the logger also sees a non-2xx response.
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusBadRequest)
	}))

	const secretName = "Patientin Geheim"
	const secretReason = "private free-text reason"
	body := `{"first_name":"` + secretName + `","reason":"` + secretReason + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/patients?q=SecretSearchTerm", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(httptest.NewRecorder(), request)

	logged := buf.String()
	for _, secret := range []string{secretName, secretReason, "SecretSearchTerm"} {
		if strings.Contains(logged, secret) {
			t.Errorf("log output contains sensitive data %q: %s", secret, logged)
		}
	}
	if !strings.Contains(logged, "POST") || !strings.Contains(logged, "/api/patients") {
		t.Errorf("log output is missing the method and path: %s", logged)
	}
	if !strings.Contains(logged, "status=400") {
		t.Errorf("log output is missing the response status: %s", logged)
	}
}
