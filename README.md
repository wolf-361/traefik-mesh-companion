# Traefik Mesh Companion

![Docker Size](https://img.shields.io/badge/size-10.2MB-blue)
![Go Version](https://img.shields.io/badge/Go-1.26.1-00ADD8)
![License](https://img.shields.io/badge/license-GNU%20GPLv3-green)

Traefik Mesh Companion is a lightweight, automated DNS synchronizer for Traefik. It monitors the local Docker socket and synchronizes Traefik routing labels to external DNS providers and Mesh VPN networks.

It is designed to facilitate Split-Horizon DNS in edge deployments, allowing traffic to be routed privately through a Mesh VPN or publicly through a standard proxy based on container-level labels.

## Architecture: Split-Horizon Routing

The companion operates using a dual-pipeline architecture, allowing a single instance to manage both internal and external DNS records simultaneously:

1. **Internal Pipeline:** Monitors specific Traefik labels (e.g., `entrypoints=internal`) and synchronizes those hosts to a Mesh VPN provider (e.g., NetBird).
2. **External Pipeline:** Monitors public-facing labels (e.g., `entrypoints=https`) and synchronizes those hosts to a public DNS provider (e.g., Cloudflare), supporting both standard A-records and CNAMEs for secure tunnels.

## Features
* **Zero-Config Auto-Discovery:** Automatically detects and maps your Cloudflare and NetBird DNS zones. No need to hardcode Zone IDs.
* **Label-Based Filtering:** Utilizes standard Traefik labels. No custom labels are required on your application containers.
* **Safe Orphan Cleanup:** Automatically removes stale DNS records when containers are destroyed, protected by a strict Target Lock to prevent accidental deletion of manually managed records.
* **Manual Overrides:** Explicitly ignore specific containers using the `mesh.managed=false` label.
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
```
> **Security Note:** For production deployments, it is highly recommended to use a Docker Socket Proxy (like `tecnativa/docker-socket-proxy`) instead of mounting `/var/run/docker.sock` directly. Set `DOCKER_HOST="tcp://proxy:2375"` in your environment variables to connect.

## Configuration Reference

### Global & Pipeline Settings

| Variable | Default | Description |
| :--- | :--- | :--- |
| `SYNC_INTERVAL` | `1m` | Interval for the full background synchronization loop. |
| `INTERNAL_PROVIDER` | `netbird` | Provider for the internal network. Set to `none` to disable. |
| `INTERNAL_FILTER` | `traefik` | The Traefik label value that triggers an internal sync. |
| `INTERNAL_FILTER_LABEL` | `traefik.http.routers.*.entrypoints` | The Traefik label pattern to monitor for the internal pipeline. |
| `INTERNAL_CLEANUP` | `false` | Enable automatic deletion of orphaned NetBird records. |
| `EXTERNAL_PROVIDER` | `none` | Provider for the external network (e.g., `cloudflare`). |
| `EXTERNAL_FILTER` | `https` | The Traefik label value that triggers an external sync. |
| `EXTERNAL_FILTER_LABEL` | `traefik.http.routers.*.entrypoints` | The Traefik label pattern to monitor for the external pipeline. |
| `EXTERNAL_CLEANUP` | `false` | Enable automatic deletion of orphaned Cloudflare records. |

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

## 🏷️ Manual Overrides
If you have a container that matches your filter (e.g., it uses the `https` entrypoint) but you **do not** want the companion to manage its DNS records, you can add the following Traefik label to that specific container:

```yaml
labels:
  - "traefik.http.routers.my-app.mesh.managed=false"
```
The companion will ignore this router entirely, ensuring it neither creates nor deletes records for it.

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

The project utilizes a standard provider interface. To add support for additional DNS or VPN providers, implement the `DNSProvider` interface located in `internal/provider/` and submit a Pull Request.

```go
type DNSProvider interface {
	Init(cfg *config.Config) error
	Sync(activeHosts map[string]bool, ignoredHosts map[string]bool, target string, cleanup bool) error
}
```

## License
This project is licensed under the GNU General Public License v3.0. See the [LICENSE](LICENSE) file for details.