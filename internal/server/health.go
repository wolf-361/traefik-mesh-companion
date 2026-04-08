package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic" // <-- Added this
	"time"
)

const (
	Port = ":9999"
	Path = "/health"
	URL  = "http://127.0.0.1" + Port + Path
)

type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
	version    string
	isHealthy  *atomic.Bool
}

func NewServer(logger *slog.Logger, version string, isHealthy *atomic.Bool) *Server {
	mux := http.NewServeMux()

	mux.HandleFunc(Path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Check the atomic flag
		healthy := isHealthy.Load()

		statusStr := "healthy"
		statusCode := http.StatusOK

		if !healthy {
			statusStr = "unhealthy"
			statusCode = http.StatusServiceUnavailable // 503 Error
		}

		w.WriteHeader(statusCode)

		if err := json.NewEncoder(w).Encode(map[string]string{
			"status":  statusStr,
			"service": "traefik-mesh-companion",
			"version": version,
		}); err != nil {
			logger.Error("Failed to encode health response", slog.Any("error", err))
		}
	})

	return &Server{
		httpServer: &http.Server{
			Addr:    Port,
			Handler: mux,
		},
		logger:    logger,
		version:   version,
		isHealthy: isHealthy,
	}
}

// Start boots the server. It blocks until stopped.
func (s *Server) Start() error {
	s.logger.Debug("Starting background health server", "port", Port, "version", s.version)
	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Stop gracefully shuts down the health server
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Debug("Shutting down background health server")
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
