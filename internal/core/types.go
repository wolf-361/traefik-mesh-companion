package core

// Service represents a normalized Traefik application discovered in Docker
type Service struct {
	ContainerName string
	Hosts         []string          // All extracted domains (e.g., app.internal.ca)
	Labels        map[string]string // The raw Docker labels for processors to filter
}

// Processor is the interface all integrations (NetBird, Kuma) must implement
type Processor interface {
	Name() string
	Process(services []Service) error
}
