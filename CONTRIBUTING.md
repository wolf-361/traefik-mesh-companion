# Contributing to Traefik Mesh Companion

First off, thank you for considering contributing to Traefik Mesh Companion! It's people like you that make the open-source homelab and infrastructure community so powerful.

## 🤝 How Can I Contribute?

### Reporting Bugs
* Ensure the bug was not already reported by searching on GitHub under [Issues].
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
* **Go 1.22** or newer.
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
3. Run it locally (You can pass a fake Docker socket or run it against your local Docker daemon):
   ```bash
   DNS_PROVIDER=netbird TARGET_IP=127.0.0.1 go run cmd/companion/main.go
   ```

---

## 🔌 How to Add a New DNS Provider

This project was built specifically to make adding new mesh networks and DNS providers (like Tailscale, Pi-hole, Technitium) as easy as possible. 

You do **not** need to touch the core Docker watching logic to add a new provider.

1. **Create a new file** in `internal/provider/` (e.g., `tailscale.go`).
2. **Implement the `DNSProvider` interface:**
   ```go
   package provider

   type TailscaleProvider struct {
       // your internal state
   }

   func (t *TailscaleProvider) Init(cfg *config.Config) error {
       // Connect to the API, validate tokens, etc.
       return nil
   }

   func (t *TailscaleProvider) Sync(activeHosts map[string]bool, targetIP string) error {
       // 1. Fetch current records
       // 2. Compare against activeHosts
       // 3. Upsert missing records and delete stale ones
       return nil
   }
   ```
3. **Register your provider** in `cmd/companion/main.go` inside the factory switch statement:
   ```go
   switch cfg.Provider {
   case "netbird":
       provider = &provider.NetbirdProvider{}
   case "tailscale":
       provider = &provider.TailscaleProvider{} // <-- Add this
   }
   ```
4. **Update the README.md** to document any new environment variables your provider requires (e.g., `TAILSCALE_API_TOKEN`).

## 📝 Styleguides

### Git Commit Messages
* Use the present tense ("Add feature" not "Added feature").
* Use the imperative mood ("Move cursor to..." not "Moves cursor to...").
* Limit the first line to 72 characters or less.
* Reference issues and pull requests liberally after the first line.

### Go Styleguide
* All Go code must be formatted using `gofmt`.
* We strictly avoid `utils` or `helpers` packages. Place code in a domain-specific package that describes *what* it does.