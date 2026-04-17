# Traefik Mesh Companion

![Docker Size](https://img.shields.io/badge/size-16.4MB-blue)
![Go Version](https://img.shields.io/badge/Go-1.26.1-00ADD8)
![Version](https://img.shields.io/github/v/release/wolf-infra/traefik-mesh-companion?color=orange)
![License](https://img.shields.io/badge/license-GNU%20GPLv3-green)

Traefik Mesh Companion is a lightweight, automated synchronizer for Traefik. It monitors the local Docker socket and automatically synchronizes Traefik routing labels to external DNS providers, Mesh VPN networks, and Uptime Monitoring services.

It is designed to facilitate Split-Horizon DNS in edge deployments and fully automate infrastructure observability without requiring manual dashboard configuration.

## Architecture

The companion operates using a multi-pipeline capability architecture, allowing a single instance to manage internal DNS, external DNS, and monitoring simultaneously:

1. **Internal Pipeline (DNS):** Monitors specific Traefik labels (e.g., `entrypoints=internal`) and synchronizes those hosts to a Mesh VPN provider (e.g., NetBird).
2. **External Pipeline (DNS):** Monitors public-facing labels (e.g., `entrypoints=https`) and synchronizes those hosts to a public DNS provider (e.g., Cloudflare), supporting both standard A-records and CNAMEs for secure tunnels.
3. **Monitoring Pipeline:** Dynamically creates, groups, and tracks HTTP monitors in Uptime Kuma based on discovered Traefik routers. 
4. **Distributed Coordinator:** Operates in a Server/Client topology to prevent API race conditions when managing Uptime Kuma UI states across multiple edge nodes simultaneously.

## Features
* **Zero-Config Auto-Discovery via AST:** Natively parses complex Traefik routing rules (including nested `Host`, `PathPrefix`, `&&`, and `||` logic) into an Abstract Syntax Tree to automatically build pixel-perfect monitor URLs.
* **Global URL Deduplication:** Smartly evaluates generated endpoints across all containers. If multiple Traefik routers resolve to the exact same URL, the companion groups them into a single Uptime Kuma monitor to prevent API spam.
* **Dynamic Status Pages & Domain Binding:** Automatically provisions Uptime Kuma Status Pages and Groups on the fly. You can dynamically bind Traefik router domains directly to Uptime Kuma Status Pages using a single label.
* **Smart Auto-Color Tagging:** Automatically injects UI tags (like the server hostname) into Uptime Kuma monitors, utilizing a deterministic hash (`djb2`) to assign distinct, repeatable colors across your mesh without manual configuration.
* **Safe Orphan Cleanup:** Automatically removes stale DNS records when containers are destroyed, protected by a strict Target Lock to prevent accidental deletion of manually managed records.
* **Traefik-Safe Overrides:** Fine-tune specific routers (custom paths like `/health`, intervals, expected status codes) using a custom `mesh.routers.*` namespace that bypasses Traefik's strict schema validator.
* **Highly Optimized Execution:** Safely supports synchronization intervals as low as 60 seconds. The watcher engine employs in-memory state caching to eliminate unnecessary API calls.
* **Ultra-Low Footprint:** Compiled as a statically linked Go binary running in a distroless `scratch` container.

## Quick Start (Docker Compose)

Deploy the companion as a sidecar alongside your Traefik instance. 

```yaml
services:
  mesh-companion:
    image: ghcr.io/wolf-infra/traefik-mesh-companion:stable
    container_name: traefik-mesh-companion
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    environment:
      - SYNC_INTERVAL=1m

      # --- Internal Pipeline (NetBird) ---
      - INTERNAL_PROVIDER=netbird
      - INTERNAL_FILTER=internal
      - INTERNAL_CLEANUP=true
      - NETBIRD_API_TOKEN=your_netbird_token
      - NETBIRD_TARGET_IP=100.64.0.5

      # --- External Pipeline (Cloudflare) ---
      - EXTERNAL_PROVIDER=cloudflare
      - EXTERNAL_FILTER=https
      - EXTERNAL_CLEANUP=true
      - CLOUDFLARE_API_TOKEN=your_cf_token
      - CLOUDFLARE_TARGET_DOMAIN=your-tunnel-uuid.cfargotunnel.com

      # --- Monitoring (Uptime Kuma) ---
      - MONITOR_PROVIDER=kuma
      - KUMA_URL=[http://kuma.your-address.ca](http://kuma.your-address.ca)
      - KUMA_USERNAME=admin
      - KUMA_PASSWORD=${KUMA_PASS}
      - KUMA_AUTO_ENABLE=true
      - KUMA_GLOBAL_STATUS_PAGE=home-lab
      - KUMA_GLOBAL_STATUS_PAGE_DOMAIN=status.your-domain.ca
```

## 🌐 Advanced Setup: Distributed Mesh (Coordinator Pattern)

When running the companion across multiple edge servers (e.g., `roma`, `firenze`), you will encounter an Uptime Kuma API race condition if all nodes attempt to write to the Status Page UI simultaneously. 

To fix this, the companion features a built-in **Coordinator Server**. Designate one server as the Master (`server`), and all others as `client`. Clients will provision their monitors locally, but forward their UI layout instructions to the server to process sequentially.

**Node 1 (The Server / Hub):**
```yaml
      # Bind port 8081 for incoming client requests
      KUMA_COORDINATOR_MODE: "server"
      KUMA_COORDINATOR_PORT: "8081"
```

**Node 2+ (The Clients / Workers):**
```yaml
      # Clients default to "client" mode. Just point them to the hub.
      KUMA_COORDINATOR_URL: "http://<SERVER_IP>:8081"
```

## Configuration Reference

### Global & Pipeline Settings

| Variable | Default | Description |
| :--- | :--- | :--- |
| `SYNC_INTERVAL` | `1m` | Interval for the full background synchronization loop. |
| `DRY_RUN` | `false` | If true, logs API actions instead of executing them. |
| `LOG_LEVEL` | `info` | Logging verbosity (`debug`, `info`, `warn`, `error`). |
| `INTERNAL_PROVIDER` | `netbird` | Provider for the internal network. Set to `none` to disable. |
| `INTERNAL_FILTER` | `traefik` | The Traefik label value that triggers an internal sync. |
| `INTERNAL_CLEANUP` | `false` | Enable automatic deletion of orphaned internal records. |
| `EXTERNAL_PROVIDER` | `none` | Provider for the external network (e.g., `cloudflare`). |
| `EXTERNAL_FILTER` | `https` | The Traefik label value that triggers an external sync. |
| `EXTERNAL_CLEANUP` | `false` | Enable automatic deletion of orphaned external records. |

### Monitoring Providers

#### Uptime Kuma Global Defaults
These environment variables define the Global Defaults enforced across the mesh when service-specific labels are missing.

| Variable | Default | Description |
| :--- | :--- | :--- |
| `MONITOR_PROVIDER` | - | Set to `kuma` to enable the provider. |
| `KUMA_URL` | - | The base URL of your Uptime Kuma instance. |
| `KUMA_USERNAME` | - | Admin username for Socket.io authentication. |
| `KUMA_PASSWORD` | - | Admin password for Socket.io authentication. |
| `KUMA_AUTO_ENABLE` | `false` | If `true`, all discovered services are monitored by default. |
| `KUMA_COORDINATOR_MODE`| `client` | Determines if this instance processes the queue (`server`) or delegates (`client`). |
| `KUMA_COORDINATOR_URL` | - | The URL of the Master Coordinator (e.g., `http://roma:8081`). |
| `KUMA_COORDINATOR_PORT`| `8080` | The port the Coordinator listens on (if acting as `server`). |
| `KUMA_GLOBAL_STATUS_PAGE` | `none` | Default status page slug for all new monitors (auto-created). |
| `KUMA_GLOBAL_STATUS_PAGE_DOMAIN` | - | Auto-enforces a clean URL mapping (FQDN only, no `https://`) on the global page. |
| `KUMA_DEFAULT_TAGS` | - | Comma-separated list of tags to inject into every monitor created by this node (e.g., `roma`, `prod:red`). |
| `KUMA_DEFAULT_INTERVAL` | `60` | Global check interval in seconds. |

---

## 🏷️ Manual Overrides & Labels

You can fine-tune how the companion interacts with specific containers by adding labels to your Docker services. 

### The Label Hierarchy
To prevent breaking Traefik's strict internal schema validation, **do not place mesh overrides inside the `traefik.*` namespace.** The Companion utilizes a fallback hierarchy:
1. **Router-Specific (Highest Priority):** `mesh.routers.<router_name>.kuma.<property>`
2. **Global Service Fallback:** `mesh.kuma.<property>`

*Example:*
```yaml
      # Traefik Router Definition
      traefik.http.routers.api.rule: "Host(`api.wolf.ca`)"
      
      # Overrides apply strictly to the "api" router
      mesh.routers.api.kuma.url: "/health"
      mesh.routers.api.kuma.accepted_status_codes: "200, 401"
      
      # Global Service definitions (Applies to all routers on this container)
      mesh.kuma.tags: "backend, prod"
```

### 📊 Uptime Kuma Override Properties

Apply these suffixes to the hierarchy above to override global defaults.

| Property | Example | Description |
| :--- | :--- | :--- |
| `enable` | `true` | Explicitly enable or disable monitoring for this router/service. |
| `pages` | `public:Websites, lab` | Comma-separated list of Status Page slugs to attach to. Supports optional `slug:Group Name` syntax. |
| `group` | `Databases` | The default target category/group for status pages if not explicitly defined in `pages`. |
| `tags` | `frontend, prod:red` | Apply UI tags. Use `tagName` for auto-colors, or `tagName:colorCode` for strict overrides. |
| `hide_status` | `true` | Monitors the service internally but completely hides it from all Status Pages. |
| `name` | `Auth API` | Override the monitor display name (defaults to container name). |
| `url` | `/health` or `http://x` | **Relative:** Appends to the AST-discovered URL (e.g. `api.com/health`).<br>**Absolute:** Overrides the URL entirely. |
| `status_page_binding` | `home-lab` | Extracts the router's Domain and automatically attaches it as a custom domain for the targeted Uptime Kuma Status Page. |
| `allow_duplicates` | `true` | Bypasses the Global URL Deduplicator, forcing the creation of a distinct monitor even if the URL is already tracked. |
| `method` | `POST` | HTTP method to use for the probe. |
| `body` | `{"test":true}` | Request body to send with the probe. |
| `headers` | `{"X-Key":"val"}` | JSON string of custom headers to include. |
| `ignore_tls` | `true` | Ignore TLS/SSL certificate validation errors. |
| `interval` | `30` | Per-service check interval in seconds. |
| `accepted_status_codes` | `200, 302` | Enforce specific status codes (comma-separated). |

### 🌐 DNS Overrides
* `mesh.routers.<name>.managed=false`: Completely ignore this router for all pipelines (DNS & Monitoring).
* `mesh.dns.internal`: `true/false` to force-enable/disable internal Mesh DNS sync.
* `mesh.dns.external`: `true/false` to force-enable/disable public Cloudflare sync.

## 📦 Releases & Versioning

This project follows [Semantic Versioning](https://semver.org/). Images are automatically built and pushed to **GitHub Container Registry (GHCR)** via GitHub Actions.

| Tag | Usage | Stability |
| :--- | :--- | :--- |
| `latest` | Tracks the `main` branch. Use for testing new features. | Experimental |
| `stable` | Points to the most recent tagged release. | Recommended |
| `vX.Y.Z` | A specific immutable version (e.g., `v1.0.0`). | Production |

### How to Release
To trigger a new stable build, simply tag your commit and push it:
```bash
git tag v1.0.0
git push origin v1.0.0
```

## Contributing

The project utilizes a Capability-Driven architectural layout. To add support for additional DNS or Monitoring providers, implement the `core.Processor` interface located in `internal/core/types.go` and place your new integration in either the `internal/dns/` or `internal/monitor/` directories.

```go
// internal/core/types.go
type Processor interface {
    Name() string
    Process(services []Service) error
}

// internal/monitor/types.go
type Provider interface {
    core.Processor
    SyncState() error
}
```

## License
This project is licensed under the GNU General Public License v3.0. See the [LICENSE](LICENSE) file for details.