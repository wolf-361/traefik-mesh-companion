package watcher

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/wolf-361/traefik-mesh-companion/internal/config"
	"github.com/wolf-361/traefik-mesh-companion/internal/core"
)

// --- MOCKS ---
type MockDockerClient struct {
	Containers []container.Summary
	ListErr    error
	MsgChan    chan events.Message
	ErrChan    chan error
}

func (m *MockDockerClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	return m.Containers, m.ListErr
}

func (m *MockDockerClient) Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error) {
	return m.MsgChan, m.ErrChan
}

type MockProcessor struct {
	ProcessedServices []core.Service
	ReturnErr         error
}

func (m *MockProcessor) Name() string { return "Mock" }
func (m *MockProcessor) Process(services []core.Service) error {
	m.ProcessedServices = services
	return m.ReturnErr
}

// --- TESTS ---
func TestWatcher_SyncAll_Success(t *testing.T) {
	mockDocker := &MockDockerClient{
		Containers: []container.Summary{
			{
				Names: []string{"/app-1"},
				Labels: map[string]string{
					"traefik.enable":                "true",
					"traefik.http.routers.web.rule": "Host(`app.local`, `app.remote`)",
				},
			},
			{
				Names: []string{"/ignored-app"},
				Labels: map[string]string{
					"traefik.enable": "false",
				},
			},
		},
	}
	mockProc := &MockProcessor{}
	var isHealthy atomic.Bool

	watcher := &Watcher{
		cli:        mockDocker,
		cfg:        &config.Config{},
		processors: []core.Processor{mockProc},
		isHealthy:  &isHealthy,
	}

	watcher.SyncAll()

	// 1. Check Health State
	if !isHealthy.Load() {
		t.Errorf("Expected health status to be true")
	}

	// 2. Check Data Extraction
	if len(mockProc.ProcessedServices) != 1 {
		t.Fatalf("Expected 1 processed service, got %d", len(mockProc.ProcessedServices))
	}

	svc := mockProc.ProcessedServices[0]
	if svc.ContainerName != "app-1" {
		t.Errorf("Expected container name 'app-1', got '%s'", svc.ContainerName)
	}

	expectedHosts := []string{"app.local", "app.remote"}
	if !reflect.DeepEqual(svc.Hosts, expectedHosts) {
		t.Errorf("Expected hosts %v, got %v", expectedHosts, svc.Hosts)
	}
}

func TestWatcher_SyncAll_DockerFailure(t *testing.T) {
	mockDocker := &MockDockerClient{
		ListErr: errors.New("docker socket disconnected"),
	}
	var isHealthy atomic.Bool
	isHealthy.Store(true) // Start healthy

	watcher := &Watcher{
		cli:        mockDocker,
		cfg:        &config.Config{},
		processors: []core.Processor{},
		isHealthy:  &isHealthy,
	}

	watcher.SyncAll()

	// Ensure the watcher correctly flags itself as unhealthy so the server drops to 503
	if isHealthy.Load() {
		t.Errorf("Expected health status to be false after Docker failure")
	}
}

func TestWatcher_Start_GracefulShutdown(t *testing.T) {
	mockDocker := &MockDockerClient{
		MsgChan: make(chan events.Message),
		ErrChan: make(chan error),
	}
	var isHealthy atomic.Bool

	watcher := &Watcher{
		cli:        mockDocker,
		cfg:        &config.Config{SyncInterval: 1 * time.Minute},
		processors: []core.Processor{},
		isHealthy:  &isHealthy,
	}

	// Create a context that auto-cancels after a split second
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// This should run, block for 50ms, and gracefully exit without hanging the test
	watcher.Start(ctx)
}
