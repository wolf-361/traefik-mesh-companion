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
   git clone https://github.com/wolf-361/traefik-mesh-companion.git
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
   NETBIRD_ZONE_NAME=local.dev \
   NETBIRD_TARGET_IP=127.0.0.1 \
   go run cmd/companion/main.go
   ```

---

## 🔌 How to Add a New DNS Provider

This project uses a dual-pipeline architecture. Adding a new provider (like Tailscale, Pi-hole, or AWS Route53) requires adding its configuration and implementing the standard provider interface. You do **not** need to touch the core Docker watching logic.

**1. Add Configuration (`internal/config/`)**
Create a new file (e.g., `tailscale.go`) to hold the specific credentials, and add a pointer to your struct in the main `Config` struct inside `config.go`. Ensure it is only loaded if the user explicitly enables it.

**2. Implement the `DNSProvider` interface (`internal/provider/`)**
Create a new file (e.g., `tailscale.go`) and implement the core interface:
   ```go
   package provider

   import "[github.com/wolf-361/traefik-mesh-companion/internal/config](https://github.com/wolf-361/traefik-mesh-companion/internal/config)"

   type TailscaleProvider struct {
       cfg *config.Config
   }

   func (t *TailscaleProvider) Init(cfg *config.Config) error {
       t.cfg = cfg
       // Connect to the API, validate tokens, etc.
       return nil
   }

   func (t *TailscaleProvider) Sync(activeHosts map[string]bool, target string) error {
       // target can be an IP address (A record) or a domain (CNAME)
       // 1. Fetch current records from the provider
       // 2. Compare against activeHosts
       // 3. Upsert missing records and delete stale ones
       return nil
   }
   ```

**3. Register your provider (`cmd/companion/main.go`)**
Add your new provider to the initialization registry map. This allows it to be used in either the Internal or External pipelines dynamically:
   ```go
   if cfg.Tailscale != nil {
       slog.Info("Booting Tailscale Provider...")
       ts := &provider.TailscaleProvider{}
       if err := ts.Init(cfg); err != nil {
           slog.Error("Failed to initialize Tailscale", "error", err)
           os.Exit(1)
       }
       providers["tailscale"] = ts // <-- Register it by name
   }
   ```

**4. Update the Documentation**
Update the `README.md` to document any new environment variables your provider requires (e.g., `TAILSCALE_API_TOKEN`).

## 📝 Styleguides

### Git Commit Messages
* Use the present tense ("Add feature" not "Added feature").
* Use the imperative mood ("Move cursor to..." not "Moves cursor to...").
* Limit the first line to 72 characters or less.
* Reference issues and pull requests liberally after the first line.

### Go Styleguide
* All Go code must be formatted using `gofmt`.
* We strictly avoid `utils` or `helpers` packages. Place code in a domain-specific package that describes *what* it does.