# Contributing to Traefik Mesh Companion

First off, thank you for considering contributing to Traefik Mesh Companion! It's people like you that make the open-source homelab and infrastructure community so powerful.

## 🤝 How Can I Contribute?

### Reporting Bugs
* Ensure the bug was not already reported by searching on GitHub under Issues.
* If you're unable to find an open issue addressing the problem, open a new one. Be sure to include a title and clear description, as much relevant information as possible, and a code sample or Docker Compose snippet demonstrating the expected behavior that is not occurring.

### Suggesting Enhancements
* Open a new issue with the `enhancement` label.
* Provide a clear and detailed explanation of the feature you want and why it's important.

### Pull Requests
1. Fork the repo and create your branch from `main`.
2. If you've added code that should be tested, add tests.
3. Ensure the test suite passes (`go test ./...`).
4. Format your code with `gofmt`.
5. Issue that pull request!

---

## 🛠️ Local Development Setup

To develop locally, you will need:
* **Go 1.23** or newer.
* **Docker** (to test the Docker socket watcher).

1. Clone your fork:
   ```bash
   git clone [https://github.com/wolf-361/traefik-mesh-companion.git](https://github.com/wolf-361/traefik-mesh-companion.git)
   cd traefik-mesh-companion
   ```
2. Download dependencies:
   ```bash
   go mod tidy
   ```
3. Run it locally (You can pass a fake Docker socket or run it against your local Docker daemon). You must provide the configuration for at least one pipeline:
   ```bash
   LOG_LEVEL=debug \
   INTERNAL_PROVIDER=netbird \
   NETBIRD_API_TOKEN=fake_token \
   NETBIRD_TARGET_IP=127.0.0.1 \
   go run cmd/companion/main.go
   ```

---

## 🔌 How to Add a New Integration

This project uses a **Capability-Driven Architecture** (Vertical Slices). Integrations are fully decoupled from the core application. Adding a new DNS provider (like Pi-hole or AWS Route53) or a new Monitoring tool (like Datadog) requires creating a dedicated package. You do **not** need to touch the core Docker watching logic or the global application configuration.

**1. Create a Feature Package**
Create a new folder in either `internal/dns/` or `internal/monitor/` based on the capability (e.g., `internal/dns/pihole/`).

**2. Isolate Configuration (`config.go`)**
Integrations must load their own environment variables. Do **not** add specific provider credentials to the global `config/config.go` file. Create a local config loader:
   ```go
   package pihole

   import "os"

   type Config struct {
       URL   string
       Token string
   }

   // LoadConfig returns nil if core requirements are missing, skipping initialization.
   func LoadConfig() *Config {
       token := os.Getenv("PIHOLE_TOKEN")
       if token == "" {
           return nil
       }
       return &Config{Token: token}
   }
   ```

**3. Implement `core.Processor` (`client.go`)**
Your client must implement the standard processor interface defined in `internal/core/types.go`. It must also accept the global `core.Executor` to handle API mutations safely.
   ```go
   package pihole

   import "[github.com/wolf-361/traefik-mesh-companion/internal/core](https://github.com/wolf-361/traefik-mesh-companion/internal/core)"

   // Ensure compile-time interface compliance
   var _ core.Processor = (*Client)(nil)

   type Client struct {
       cfg  *Config
       exec *core.Executor
   }

   func New(exec *core.Executor) *Client {
       cfg := LoadConfig()
       if cfg == nil { return nil }
       return &Client{cfg: cfg, exec: exec}
   }

   func (c *Client) Name() string { return "Pi-hole" }

   func (c *Client) Process(services []core.Service) error {
       // 1. Filter the Traefik labels relevant to your integration
       // 2. Diff current state against the external API

       // 3. Wrap ALL mutating API calls in the Executor to enforce DRY_RUN!
       // _ = c.exec.Run("create Pi-hole record", func() error {
       //     return myAPIClient.CreateRecord(...)
       // }, "host", "example.com")

       return nil
   }
   ```

**4. Register your provider (`cmd/companion/main.go`)**
Initialize your provider in the orchestrator. For DNS providers, add a case to the appropriate pipeline `switch` statement. For monitoring, append it directly to the processor list. **Remember to pass the global `exec` parameter:**
   ```go
   switch cfg.Internal.Provider {
   // ... existing providers ...
   case "pihole":
       slog.Debug("Booting Pi-hole Provider...")
       if ph := pihole.New(exec); ph != nil {
           processors = append(processors, ph)
       }
   }
   ```

**5. Update the Documentation**
Update the `README.md` to document any new environment variables your provider requires.

---

## 📝 Styleguides

### Git Commit Messages
* Use the present tense ("Add feature" not "Added feature").
* Use the imperative mood ("Move cursor to..." not "Moves cursor to...").
* Limit the first line to 72 characters or less.
* Reference issues and pull requests liberally after the first line.

### Go Architecture & Style
* All Go code must be formatted using `gofmt`.
* Ensure all HTTP response bodies are properly closed, wrapped in a `defer func()` to satisfy `errcheck` linters.
* **Strict Dry Run Compliance:** All state-mutating actions (POST, PUT, DELETE) *must* be wrapped in the injected `core.Executor.Run()` method. This ensures the `DRY_RUN` flag is globally respected and standardizes our logging output. Never bypass the executor to make direct mutating HTTP calls.
* **No Junk Drawers:** We strictly avoid generic `utils`, `helpers`, or `providers` packages. We use Capability-Driven Vertical Slices. Place code in a domain-specific package that describes *what* it does (e.g., `internal/dns/cloudflare`).