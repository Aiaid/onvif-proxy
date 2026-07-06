# 05 · Deployment (Docker)

## 1. Image

Multi-stage build; the final image = `alpine` + ffmpeg + a single binary, targeting a size < 120MB (ffmpeg accounts for most of it).

```dockerfile
# ---- build ----
FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/onvif-proxy ./cmd/onvif-proxy

# ---- runtime ----
FROM alpine:3.20
RUN apk add --no-cache ffmpeg tzdata ca-certificates
COPY --from=build /out/onvif-proxy /usr/local/bin/onvif-proxy
VOLUME /config
EXPOSE 8080
ENTRYPOINT ["onvif-proxy", "-config", "/config/config.yaml"]
```

Multi-arch: `linux/amd64` + `linux/arm64` (Raspberry Pi / NAS), built with `docker buildx` + GitHub Actions and published to GHCR.

### Environment variables

Global fields support env overrides (env > yaml, in-memory only, not written back to the mounted config.yaml; see `docs/03-config.en.md` §3 for the full semantics): `ONVIF_ADVERTISE_IP`, `ONVIF_DISCOVERY`, `ONVIF_WEB_ENABLED`, `ONVIF_WEB_PORT`, `ONVIF_WEB_USERNAME`, `ONVIF_WEB_PASSWORD`, and `CONFIG` (the config file path). Typical uses: setting Basic auth for the Web UI directly in the compose file, or specifying the host IP in bridge mode, without having to hand-write config.yaml beforehand.

## 2. Choosing a network mode (critical)

WS-Discovery relies on UDP multicast, which a plain bridge network cannot pass through. Three modes, in recommended order:

> The `compose.yaml` shipped at the repository root is the materialized version of the template below (host networking / mode B enabled by default); the `networks` top-level block for macvlan and the `ports` block for bridge are both kept as comments in their entirety — comment/uncomment the relevant section to switch modes as needed, without creating a separate file.

### Mode A: macvlan (recommended, cleanest for discovery + multiple devices)

The container gets its own IP/MAC on the host's subnet, so multicast, discovery, and adoption all work natively; what Unifi Protect sees is simply "another camera showing up on the network."

```yaml
# compose.yaml
services:
  onvif-proxy:
    image: ghcr.io/aiaid/onvif-proxy:latest
    restart: unless-stopped
    networks:
      lan:
        ipv4_address: 192.168.1.99      # must match advertise_ip in config.yaml
    volumes:
      - ./config:/config

networks:
  lan:
    driver: macvlan
    driver_opts:
      parent: eth0                       # the host's physical NIC
    ipam:
      config:
        - subnet: 192.168.1.0/24
          gateway: 192.168.1.1
          ip_range: 192.168.1.96/28      # small range reserved for the container
```

Note: under macvlan, **the host itself cannot reach the container's IP by default** (a kernel limitation); access the management UI from another machine on the LAN, or add a macvlan shim interface.

### Mode B: host networking (Linux host, simplest for a single container)

```yaml
services:
  onvif-proxy:
    image: ghcr.io/aiaid/onvif-proxy:latest
    network_mode: host
    restart: unless-stopped
    volumes:
      - ./config:/config
    environment:                         # optional: env overrides for global config (see docs/03 §3)
      ONVIF_WEB_USERNAME: admin
      ONVIF_WEB_PASSWORD: ${WEB_PASSWORD}
```

Multicast works; all ports are bound directly on the host, so plan around this to avoid conflicts. The same spot in compose.yaml also keeps a commented-out `ONVIF_WEB_PORT` override example (for changing the Web UI listen port) — uncomment it as needed, with no changes to config.yaml required.

### Mode C: bridge + manual add (discovery unavailable)

Map ports one by one (8080, plus each device's soap/rtsp ports), set `advertise_ip` to the host IP, and set `discovery: false`. On the client side, **manually add the ONVIF device by IP:port** (Unifi Protect supports manual entry). Suitable for Docker Desktop on Mac/Windows (whose virtualization layer can't pass multicast through anyway).

## 3. Multiple virtual devices and Unifi Protect

- Running multiple virtual devices on the same container/IP (with different soap ports) is allowed by the spec; Protect distinguishes devices by XAddr (host:port), and this has been verified to allow adopting them separately;
- If a client insists on "one IP per device," run multiple macvlan IPs, each with its own container instance (each instance with a trimmed-down config).

## 4. Upgrades and data

- The only persisted data is `/config/config.yaml` (including the auto-generated uuid/mac);
- Upgrading = swap the image and restart; persisting the uuid/mac ensures the client never mistakes the device for a new one;
- Backup = back up that single YAML file.

## 5. CI/CD (GitHub Actions, already implemented: `.github/workflows/docker-publish.yml`)

- **PR** → `go vet` + `go test -race` (test only, no build);
- **push to main / tag v\* / manual trigger** → test → amd64 + arm64 built in parallel on **native runners** (push-by-digest to GHCR, with gha cache) → a merge job combines the manifest list;
- Image target: `ghcr.io/aiaid/onvif-proxy` (via `GITHUB_TOKEN`, always available); when the repository has `DOCKERHUB_USERNAME`/`DOCKERHUB_TOKEN` secrets configured, the same set of tags is automatically mirrored to Docker Hub `<user>/onvif-proxy` (imagetools copies the blobs cross-registry, no rebuild needed);
- Tag rules: branch name, `v*` versions (+ semver), short sha, and `latest` on main; the `VERSION` build-arg is injected into the binary (tag name or short sha).

## 6. Running bare-metal (without Docker)

```bash
go build -o onvif-proxy ./cmd/onvif-proxy
./onvif-proxy -config config.yaml      # requires ffmpeg on PATH (used only for snapshots/preview)
```

Developing and debugging on macOS is entirely workable (multicast discovery works within the same LAN); Linux is recommended for production.
