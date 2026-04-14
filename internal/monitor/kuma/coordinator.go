package kuma

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

type AttachPayload struct {
	MonitorID   int64             `json:"monitor_id"`
	MonitorName string            `json:"monitor_name"`
	Hosts       []string          `json:"hosts"`
	Labels      map[string]string `json:"labels"`
}

type Coordinator struct {
	cfg           *Config
	statusManager *StatusPageManager
	attachQueue   chan AttachPayload
}

func NewCoordinator(cfg *Config, sm *StatusPageManager) *Coordinator {
	c := &Coordinator{
		cfg:           cfg,
		statusManager: sm,
		// Buffer up to 100 requests to prevent blocking
		attachQueue:   make(chan AttachPayload, 100), 
	}

	if cfg.CoordinatorMode == "server" {
		go c.startServer()
		go c.processQueue()
	}

	return c
}

// processQueue ensures only ONE status page update happens at a time
func (c *Coordinator) processQueue() {
	for payload := range c.attachQueue {
		slog.Info("Coordinator processing queued attach request", "monitor", payload.MonitorName)
		c.statusManager.ProcessStatusPages(context.Background(), payload.MonitorID, payload.MonitorName, payload.Hosts, payload.Labels)
		// Small buffer to let Uptime Kuma's database settle
		time.Sleep(500 * time.Millisecond)
	}
}

func (c *Coordinator) startServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/kuma/attach", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var payload AttachPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		// Push to the queue
		c.attachQueue <- payload
		w.WriteHeader(http.StatusAccepted)
	})

	addr := ":" + c.cfg.CoordinatorPort
	slog.Info("Starting Kuma Coordinator Master API", "port", c.cfg.CoordinatorPort)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("Coordinator server failed", "error", err)
	}
}

func (c *Coordinator) RequestAttach(payload AttachPayload) {
	// If we are the server, process locally/queue it
	if c.cfg.CoordinatorMode == "server" {
		c.attachQueue <- payload
		return
	}

	// If we are a client, send the request to the server	
	body, _ := json.Marshal(payload)
	url := c.cfg.CoordinatorURL + "/api/kuma/attach"

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		slog.Error("Client failed to reach Coordinator Server", "url", url, "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		slog.Warn("Coordinator Server rejected request", "status", resp.StatusCode)
	} else {
		slog.Debug("Delegated status page attachment to Server", "monitor", payload.MonitorName)
	}
}