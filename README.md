# Traefik Mesh Companion

![Docker Size](https://img.shields.io/badge/size-10.2MB-blue)
![Go Version](https://img.shields.io/badge/Go-1.26.1-00ADD8)
![License](https://img.shields.io/badge/license-GNU%20GPLv3-green)

Traefik Mesh Companion is a lightweight, automated DNS synchronizer for Traefik. It monitors the local Docker socket and synchronizes Traefik routing labels to external DNS providers and Mesh VPN networks.

It is designed to facilitate Split-Horizon DNS in edge deployments, allowing traffic to be routed privately through a Mesh VPN or publicly through a standard proxy based on container-level labels.

## Architecture: Split-Horizon Routing

The companion operates using a dual-pipeline architecture, allowing a single instance to manage both internal and external DNS records simultaneously:

1. **Internal Pipeline:** Monitors specific Traefik entrypoints (e.g., `traefik` or `internal`) and synchronizes those hosts to a Mesh VPN provider (e.g., NetBird).
2. **External Pipeline:** Monitors public-facing entrypoints (e.g., `https`) and synchronizes those hosts to a public DNS provider (e.g., Cloudflare), supporting both standard A-records and CNAMEs for secure tunnels.

## Features
* **Label-Based Filtering:** Utilizes standard Traefik labels. No custom labels are required on your application containers.
* **Extensible Provider Interface:** Modular Go architecture designed to support multiple DNS and VPN backends.
* **Automated Lifecycle Management:** Creates records when containers start and removes stale records when containers are destroyed.
* **Highly Optimized Execution:** Safely supports synchronization intervals as low as 60 seconds. The watcher engine employs in-memory state caching and pre-compiled regex filters. External APIs are only invoked if a true container state change is detected, eliminating unnecessary network calls and mitigating API rate-limiting risks.
* **Low Resource Footprint:** Compiled as a static Go binary running in a scratch container.

## Quick Start (Docker Compose)

Deploy the companion as a sidecar alongside your Traefik instance. The following example demonstrates a dual-provider configuration using NetBird for internal routing and Cloudflare for external proxying.

```yaml
services:
  mesh-companion:
    image: ghcr.io/wolf-361/traefik-mesh-companion:stable
    container_name: traefik-mesh-companion
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    environment:
      # --- Global Settings ---
      - SYNC_INTERVAL=1m

      # --- Internal Pipeline (NetBird) ---
      - INTERNAL_PROVIDER=netbird
      - INTERNAL_FILTER=traefik
      - NETBIRD_API_TOKEN=your_netbird_token
      - NETBIRD_ZONE_NAME=mesh.internal.com
      - NETBIRD_TARGET_IP=100.64.0.5

      # --- External Pipeline (Cloudflare) ---
      - EXTERNAL_PROVIDER=cloudflare
      - EXTERNAL_FILTER=https
      - CLOUDFLARE_API_TOKEN=your_cf_token
      - CLOUDFLARE_ZONE_ID=your_zone_id
      - CLOUDFLARE_TARGET_DOMAIN=your-tunnel-uuid.cfargotunnel.com
```

## Configuration Reference

### Global & Pipeline Settings

| Variable | Default | Description |
| :--- | :--- | :--- |
| `SYNC_INTERVAL` | `1m` | Interval for the full background synchronization loop. |
| `INTERNAL_PROVIDER` | `netbird` | Provider for the internal network. Set to `none` to disable. |
| `INTERNAL_FILTER` | `traefik` | The Traefik entrypoint value that triggers an internal sync. |
| `INTERNAL_FILTER_LABEL` | `traefik.http.routers.*.entrypoints` | The Traefik label pattern to monitor for the internal pipeline. |
| `EXTERNAL_PROVIDER` | `none` | Provider for the external network (e.g., `cloudflare`). |
| `EXTERNAL_FILTER` | `https` | The Traefik entrypoint value that triggers an external sync. |
| `EXTERNAL_FILTER_LABEL` | `traefik.http.routers.*.entrypoints` | The Traefik label pattern to monitor for the external pipeline. |

### Provider Credentials

#### NetBird
| Variable | Description |
| :--- | :--- |
| `NETBIRD_API_TOKEN` | Personal Access Token for API authentication. |
| `NETBIRD_ZONE_NAME` | The DNS Zone managed within the NetBird interface. |
| `NETBIRD_TARGET_IP` | The local machine's Mesh IP address. |
| `NETBIRD_API_URL` | *(Optional)* Override for self-hosted instances. Defaults to `https://api.netbird.io/api`. |

#### Cloudflare
| Variable | Description |
| :--- | :--- |
| `CLOUDFLARE_API_TOKEN` | API Token with `Zone.DNS` edit permissions. |
| `CLOUDFLARE_ZONE_ID` | The 32-character Zone ID for the target domain. |
| `CLOUDFLARE_TARGET_DOMAIN` | The target destination. Use a CNAME (e.g., Argo Tunnel) or an IP address. |

## 📦 Releases & Versioning

This project follows [Semantic Versioning](https://semver.org/). Images are automatically built and pushed to **GitHub Container Registry (GHCR)** via GitHub Actions.

| Tag | Usage | Stability |
| :--- | :--- | :--- |
| `latest` | Tracks the `main` branch. Use for testing new features. |  Experimental |
| `stable` | Points to the most recent tagged release. |  Recommended |
| `vX.Y.Z` | A specific immutable version (e.g., `v1.0.0`). |  Production |
| `sha-xxxx` | Every commit generates a unique short-SHA tag. |  Debugging |

To use the stable version in your `docker-compose.yml`:

```yaml
services:
  companion:
    image: ghcr.io/wolf-361/traefik-mesh-companion:stable
    # ... rest of config
```

### 🏷️ How to Release
To trigger a new stable build, simply tag your commit and push it:

```bash
git tag v1.0.0
git push origin v1.0.0
```

## Contributing

The project utilizes a standard provider interface. To add support for additional DNS or VPN providers, implement the `DNSProvider` interface located in `internal/provider/` and submit a Pull Request.

```go
type DNSProvider interface {
  Init(cfg *config.Config) error
  Sync(activeHosts map[string]bool, targetIP string) error
}
```

## License
This project is licensed under the GNU General Public License v3.0. See the [LICENSE](LICENSE) file for details.