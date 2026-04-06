package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	Port = ":9999"
	Path = "/health"
	URL  = "http://127.0.0.1" + Port + Path
)

// Server wraps the standard HTTP server for health checks
type Server struct {
	httpServer *http.Server
}

// NewServer initializes the health check HTTP server
func NewServer() *Server {
	mux := http.NewServeMux()
	mux.HandleFunc(Path, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	return &Server{
		httpServer: &http.Server{
			Addr:    Port,
			Handler: mux,
		},
	}
}

// Start boots the server. It blocks until stopped.
func (s *Server) Start() error {
	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Stop gracefully shuts down the health server
func (s *Server) Stop(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// Check performs the actual HTTP GET used by the Docker healthcheck flag.
// It returns an error if the ping fails or times out.
func Check() error {
	// A strict 2-second timeout prevents the healthcheck from hanging
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get(URL)
	if err != nil {
		return fmt.Errorf("healthcheck failed to connect: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned unexpected status: %d", resp.StatusCode)
	}

	return nil
}
