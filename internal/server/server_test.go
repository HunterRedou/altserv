package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestNewSetsTimeouts(t *testing.T) {
	s := New(":8080", http.NewServeMux())
	srv := s.HTTPServer()

	if srv.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", srv.Addr)
	}
	if srv.ReadHeaderTimeout != readHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %s, want %s", srv.ReadHeaderTimeout, readHeaderTimeout)
	}
	if srv.ReadTimeout != readTimeout {
		t.Errorf("ReadTimeout = %s, want %s", srv.ReadTimeout, readTimeout)
	}
	if srv.WriteTimeout != writeTimeout {
		t.Errorf("WriteTimeout = %s, want %s", srv.WriteTimeout, writeTimeout)
	}
	if srv.IdleTimeout != idleTimeout {
		t.Errorf("IdleTimeout = %s, want %s", srv.IdleTimeout, idleTimeout)
	}
	if srv.MaxHeaderBytes != maxHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, maxHeaderBytes)
	}
}

func TestServeShutsDown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	s := New(listener.Addr().String(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Serve(listener)
	}()

	url := "http://" + listener.Addr().String()
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil after shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
}

func TestRunReturnsListenError(t *testing.T) {
	s := New("127.0.0.1:not-a-port", http.NewServeMux())

	if err := s.Run(context.Background()); err == nil {
		t.Fatal("Run returned nil for an invalid address")
	}
}
