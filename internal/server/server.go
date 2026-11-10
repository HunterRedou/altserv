package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 10 * time.Second
	maxHeaderBytes    = 1 << 20 // 1 MiB
)

// Server wraps http.Server with production timeouts and graceful shutdown.
type Server struct {
	httpServer *http.Server
}

// New creates a server with conservative timeouts. The caller owns the handler.
func New(addr string, handler http.Handler) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
		},
	}
}

// HTTPServer exposes the underlying server for tests and advanced wiring.
func (s *Server) HTTPServer() *http.Server {
	return s.httpServer
}

// Run listens on the configured address and serves until ctx is canceled.
// On cancellation it stops accepting connections and waits for in-flight
// requests up to shutdownTimeout.
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.httpServer.Addr, err)
	}
	return s.run(ctx, listener)
}

func (s *Server) run(ctx context.Context, listener net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Serve(listener)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := s.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return <-errCh
	}
}

// Serve serves on an existing listener. A normal shutdown is not an error.
func (s *Server) Serve(listener net.Listener) error {
	err := s.httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
