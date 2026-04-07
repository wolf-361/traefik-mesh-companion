# Traefik Mesh Companion

![Docker Size](https://img.shields.io/badge/size-10.2MB-blue)
![Go Version](https://img.shields.io/badge/Go-1.26.1-00ADD8)
![License](https://img.shields.io/badge/license-GNU%20GPLv3-green)

Traefik Mesh Companion is a lightweight, automated synchronizer for Traefik. It monitors the local Docker socket and automatically synchronizes Traefik routing labels to external DNS providers, Mesh VPN networks, and Uptime Monitoring services.

It is designed to facilitate Split-Horizon DNS in edge deployments and fully automate infrastructure observability without requiring manual dashboard configuration.

## Architecture

The companion operates using a multi-pipeline capability architecture, allowing a single instance to manage internal DNS, external DNS, and monitoring simultaneously:

1. **Internal Pipeline (DNS):** Monitors specific Traefik labels (e.g., `entrypoints=internal`) and synchronizes those hosts to a Mesh VPN provider (e.g., NetBird).
2. **External Pipeline (DNS):** Monitors public-facing labels (e.g., `entrypoints=https`) and synchronizes those hosts to a public DNS provider (e.g., Cloudflare), supporting both standard A-records and CNAMEs for secure tunnels.
3. **Monitoring Pipeline:** Dynamically creates, groups, and tracks HTTP monitors in Uptime Kuma based on discovered Traefik routers.

## Features
* **Zero-Config Auto-Discovery:** Automatically detects and maps your Cloudflare zones, NetBird zones, and Uptime Kuma groups. 
* **Label-Based Filtering:** Utilizes standard Traefik labels. No custom labels are required on your application containers for basic functionality.
* **Safe Orphan Cleanup:** Automatically removes stale DNS records when containers are destroyed, protected by a strict Target Lock to prevent accidental deletion of manually managed records.
* **Granular Overrides:** Explicitly ignore specific containers or customize ping intervals, monitor groups, and expected status codes using `mesh.*` labels.
* **Highly Optimized Execution:** Safely supports synchronization intervals as low as 60 seconds. The watcher engine employs in-memory state caching and pre-compiled regex filters to eliminate unnecessary API calls.
* **Ultra-Low Footprint:** Compiled as a statically linked Go binary running in a distroless `scratch` container.

## Quick Start (Docker Compose)

Deploy the companion as a sidecar alongside your Traefik instance. 

```yaml
services:
  mesh-companion:
    image: ghcr.io/wolf-361/traefik-mesh-companion:stable
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
      - KUMA_URL=http://uptime-kuma:3001
      - KUMA_API_KEY=your_kuma_api_key
      - KUMA_AUTO_ENABLE=true
```
> **Security Note:** For production deployments, it is highly recommended to use a Docker Socket Proxy (like `tecnativa/docker-socket-proxy`) instead of mounting `/var/run/docker.sock` directly. Set `DOCKER_HOST="tcp://proxy:2375"` in your environment variables to connect.

## 🌐 Advanced Networking: Mesh DNS & Uptime Kuma

If you are using the Companion to simultaneously manage **internal Mesh DNS** (e.g., NetBird) and **Uptime Kuma**, you must ensure that your Uptime Kuma instance can resolve your private Mesh domains. 

By default, Docker containers use standard public DNS resolvers (like `1.1.1.1` or `8.8.8.8`), which will fail to route traffic to internal `.netbird.cloud` (or custom) domains.

To fix this, you must explicitly set the DNS server of your Uptime Kuma container to your Mesh network's DNS resolver IP. 

**For NetBird (docker-compose.yml):**
```yaml
services:
  uptime-kuma:
    image: louislam/uptime-kuma:1
    container_name: uptime-kuma
    restart: always
    volumes:
      - uptime-kuma:/app/data
    dns:
      - 100.64.0.1  # The default NetBird nameserver IP
      - 1.1.1.1     # Fallback for standard public internet requests
```
*(If you are using a self-hosted NetBird control plane or a custom subnet, replace `100.64.0.1` with your specific Mesh DNS IP).*

## Configuration Reference

### Global & Pipeline Settings

| Variable | Default | Description |
| :--- | :--- | :--- |
| `SYNC_INTERVAL` | `1m` | Interval for the full background synchronization loop. |
| `DRY_RUN` | `false` | If true, logs API actions instead of executing them. |
| `INTERNAL_PROVIDER` | `netbird` | Provider for the internal network. Set to `none` to disable. |
| `INTERNAL_FILTER` | `traefik` | The Traefik label value that triggers an internal sync. |
| `INTERNAL_FILTER_LABEL` | `traefik.http.routers.*.entrypoints` | The Traefik label pattern to monitor for the internal pipeline. |
| `INTERNAL_CLEANUP` | `false` | Enable automatic deletion of orphaned internal records. |
| `EXTERNAL_PROVIDER` | `none` | Provider for the external network (e.g., `cloudflare`). |
| `EXTERNAL_FILTER` | `https` | The Traefik label value that triggers an external sync. |
| `EXTERNAL_FILTER_LABEL` | `traefik.http.routers.*.entrypoints` | The Traefik label pattern to monitor for the external pipeline. |
| `EXTERNAL_CLEANUP` | `false` | Enable automatic deletion of orphaned external records. |

### Provider Credentials

#### NetBird
| Variable | Description |
| :--- | :--- |
| `NETBIRD_API_TOKEN` | Personal Access Token for API authentication. |
| `NETBIRD_TARGET_IP` | The local machine's Mesh IP address. |
| `NETBIRD_API_URL` | *(Optional)* Override for self-hosted instances. Defaults to `https://api.netbird.io/api`. |

#### Cloudflare
| Variable | Description |
| :--- | :--- |
| `CLOUDFLARE_API_TOKEN` | API Token with `Zone.DNS` edit permissions. |
| `CLOUDFLARE_TARGET_DOMAIN` | The target destination. Use a CNAME (e.g., Argo Tunnel) or an IP address. |

#### Uptime Kuma
| Variable | Description |
| :--- | :--- |
| `KUMA_URL` | The base URL of your Uptime Kuma instance. |
| `KUMA_API_KEY` | API Key for authenticating with Kuma. |
| `KUMA_AUTO_ENABLE` | If `true`, automatically creates monitors for all discovered Traefik routers. If `false`, requires the `mesh.kuma.enable=true` label on the container. |

## 🏷️ Manual Overrides & Labels

You can fine-tune how the companion interacts with specific containers by adding these labels to your Docker services:

### DNS Overrides
* `traefik.http.routers.<name>.mesh.managed=false`: Forces the DNS pipelines to completely ignore this router (no records will be created or deleted).

### Kuma Overrides
If Kuma is configured, it automatically pulls the URL from the Traefik `Host()` rule. You can override its behavior with the following labels:
* `mesh.kuma.enable`: `true`/`false` to explicitly enable or disable monitoring for this container.
* `mesh.kuma.name`: Override the display name in Uptime Kuma (defaults to container name).
* `mesh.kuma.group`: Assign the monitor to a specific group. The companion will automatically create the group in Kuma if it does not exist.
* `mesh.kuma.url`: Override the endpoint URL to monitor.
* `mesh.kuma.interval`: Check interval in seconds (default: 60).
* `mesh.kuma.accepted_status_codes`: Comma-separated list of healthy HTTP codes (default: `200-299`).
* `mesh.kuma.ignore_tls`: `true`/`false` to skip SSL certificate validation for this monitor.

## 📦 Releases & Versioning

This project follows [Semantic Versioning](https://semver.org/). Images are automatically built and pushed to **GitHub Container Registry (GHCR)** via GitHub Actions.

| Tag | Usage | Stability |
| :--- | :--- | :--- |
| `latest` | Tracks the `main` branch. Use for testing new features. |  Experimental |
| `stable` | Points to the most recent tagged release. |  Recommended |
| `vX.Y.Z` | A specific immutable version (e.g., `v1.0.0`). |  Production |
| `sha-xxxx` | Every commit generates a unique short-SHA tag. |  Debugging |

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
```

## License
This project is licensed under the GNU General Public License v3.0. See the [LICENSE](LICENSE) file for details.