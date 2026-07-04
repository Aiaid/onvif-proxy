# onvif-proxy

An **RTSP → ONVIF virtual camera proxy** written in Go. It wraps any existing RTSP stream (IP cameras, NVR channels, Raspberry Pi, ffmpeg pushes, …) into a spec-compliant ONVIF Profile S virtual device that can be discovered and adopted by Unifi Protect, Synology Surveillance Station, Frigate, and other ONVIF clients.

> **Why another one?** The existing [daniela-hase/onvif-server](https://github.com/daniela-hase/onvif-server) and its fork [p10tyr/rtsp-to-onvif](https://github.com/p10tyr/rtsp-to-onvif) are built on the Node.js `soap` library + WSDL. Any method not explicitly registered throws a malformed bare HTTP 500 (in practice `GetCapabilities`, `GetScopes`, and `GetNetworkInterfaces` all 500), which makes some clients mark the device as incompatible. This project is a from-scratch Go rewrite that implements the full mandatory method set with standard fault semantics, plus a built-in web UI for configuration and testing.

## Features

- **Full ONVIF Profile S mandatory coverage** — every mandatory Device and Media service method is implemented; unimplemented optional methods return a spec-compliant `ter:ActionNotSupported` SOAP fault instead of a bare 500.
- **Multiple media profiles per device** — not just a high/low pair: define any number of named streams (`main` / `sub` / `mobile` / …), each exposed as its own ONVIF profile with its own encoder configuration and stream URI.
- **Snapshot support** — `GetSnapshotUri` works out of the box: pass through the real camera's HTTP snapshot URL when it has one, or let the built-in ffmpeg grabber pull a JPEG frame straight from the RTSP stream (with short-TTL caching). This fills a gap left open by the upstream projects.
- **WS-Discovery** — multicast discovery on UDP 3702 with Hello / Bye / ProbeMatches, so clients find the virtual devices automatically.
- **Zero-transcode RTSP proxy** — a per-device TCP passthrough proxy forwards the stream bytes untouched to the real camera; no decoding, no CPU cost. ffmpeg is only used for snapshots and UI previews.
- **Web UI** — besides the YAML config file, a small embedded web backend lets you edit the config online, probe RTSP connectivity (native RTSP client with Digest/Basic auth and SDP parsing), grab snapshots, watch a live MJPEG preview, and run an ONVIF self-test against the virtual device itself.
- **Docker-first deployment** — multi-stage build with ffmpeg baked in; macvlan compose example so each virtual device can get its own IP/MAC on your LAN.
- **Single binary, minimal dependencies** — only `gopkg.in/yaml.v3`; SOAP messages are hand-written XML templates, no WSDL code generation.

## Documentation

Design docs (currently in Chinese; the code and config schema are English):

| Doc | Contents |
|-----|----------|
| [docs/01-architecture.md](docs/01-architecture.md) | Architecture, module layout, data flows, directory structure |
| [docs/02-onvif-spec.md](docs/02-onvif-spec.md) | ONVIF / SOAP / WS-Discovery / WSSE conformance design, method matrix, fault semantics |
| [docs/03-config.md](docs/03-config.md) | YAML config format and validation rules |
| [docs/04-web-api.md](docs/04-web-api.md) | Web backend REST API and UI design |
| [docs/05-deployment.md](docs/05-deployment.md) | Docker / macvlan deployment |

## Quick start (planned)

```bash
# 1. Prepare a config
cp config.example.yaml config.yaml && vim config.yaml

# 2. Run
docker compose up -d

# 3. Open the web UI to verify
open http://<host>:8080
```

## Status

- [x] Design documents
- [ ] Core implementation (SOAP services, WS-Discovery, RTSP proxy)
- [ ] Web backend and UI
- [ ] Docker image and compose
- [ ] Verification against Unifi Protect

## License

MIT
