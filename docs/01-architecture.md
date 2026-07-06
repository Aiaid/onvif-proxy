# 01 · Architecture

**English** | [简体中文](01-architecture.zh-CN.md)

## 1. Design Goals

1. **Spec-first**: strictly implement the ONVIF Core Spec / Media Service Spec / SOAP 1.2 / WS-Discovery; every response (including errors) is a well-formed SOAP message — no "unimplemented method → bare HTTP 500".
2. **Zero transcoding**: video streams pass through over TCP; the proxy itself never decodes or transcodes. ffmpeg is only used for low-frequency operations such as snapshots and UI preview.
3. **Single binary**: the Go build output is self-contained (Web UI static assets are baked into the binary via `go:embed`); the Docker image only needs to add ffmpeg on top.
4. **Config is the source of truth**: the YAML config file is the only persisted state; Web UI edits are ultimately flushed to the same YAML, surviving restarts without loss.

## 2. In-Process Components

```
                                ┌────────────────────────────────────────────┐
                                │            onvif-proxy (single process)     │
                                │                                            │
 ONVIF client                   │  ┌──────────────┐      ┌────────────────┐  │
 (Unifi Protect…)               │  │ WS-Discovery │      │  Config Store  │  │
    │  UDP 3702 multicast       │  │  UDP 3702    │      │  (YAML read/    │  │
    ├───────────────────────────┼─▶│  Hello/Bye/  │      │   write, valid- │  │
    │                           │  │  ProbeMatch  │      │   ation, hot    │  │
    │                           │  └──────────────┘      │   reload)       │  │
    │                           │                        └───────┬────────┘  │
    │  HTTP POST /onvif/*       │  ┌──────────────────────────┐  │           │
    ├───────────────────────────┼─▶│ Virtual Device × N       │◀─┘           │
    │                           │  │ ┌──────────────────────┐ │              │
    │                           │  │ │ SOAP HTTP Server     │ │              │
    │                           │  │ │  - Device Service    │ │              │
    │                           │  │ │  - Media Service     │ │              │
    │                           │  │ │  - /snapshot (JPEG)  │ │              │
    │  RTSP (TCP)               │  │ └──────────────────────┘ │              │
    ├───────────────────────────┼─▶│ ┌──────────────────────┐ │  TCP pass-   │    ┌──────────┐
    │                           │  │ │ RTSP TCP Proxy       │─┼─through──────┼───▶│ Real     │
    │                           │  │ └──────────────────────┘ │              │    │ camera   │
    │                           │  └──────────────────────────┘              │    │ RTSP src │
 Browser                        │  ┌──────────────────────────┐              │    └──────────┘
    │  HTTP :8080               │  │ Web Server               │              │         ▲
    └───────────────────────────┼─▶│  - REST API              │              │         │
                                │  │  - Embedded static UI     │              │         │
                                │  │    (embed)                │              │         │
                                │  │  - /mcp (12 MCP tools)    │              │         │
                                │  │  - RTSP Probe client      │─────────────┼─────────┤ RTSP DESCRIBE
                                │  │  - ffmpeg snapshot/MJPEG  │─────────────┼─────────┘ pull stream
                                │  │    preview                │              │
                                │  └──────────────────────────┘              │
                                └────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility |
|------|------|
| **Config Store** | Loads/validates/atomically writes back YAML; generates persisted default values for missing fields (UUID, MAC); publishes reload events to the Manager; after loading, applies `ApplyEnvOverrides` (`ONVIF_*` environment variable runtime overrides, in-memory only, never written back to YAML — see `internal/config/env.go`, docs/03-config.md §3) |
| **Device Manager** | Instantiates N Virtual Device instances from config; handles start/stop and hot reload (stop-then-start, port rebinding) |
| **Virtual Device** | One virtual ONVIF camera = one SOAP HTTP port + one RTSP proxy port; holds this device's profile/encoding parameters/target RTSP address |
| **SOAP layer** | Parses the SOAP Envelope (method name, WSSE header, parameters), routes to a handler, renders the XML response template; unified Fault generation |
| **WS-Discovery** | A single UDP listener answers on behalf of all virtual devices; implements Hello/Bye/Probe→ProbeMatches |
| **RTSP TCP Proxy** | `io.Copy` bidirectional pass-through to the real camera's RTSP port; RTSP auth is end-to-end (client credentials go straight through to the real camera) |
| **Web Server** | REST API + embedded UI; native RTSP probing (OPTIONS/DESCRIBE + Digest auth); invokes ffmpeg to provide snapshots and MJPEG preview; ONVIF self-test; embeds the `/mcp` endpoint, exposing 12 MCP tools (device CRUD, RTSP probe, snapshot, ONVIF self-test, etc.) — see docs/07-mcp.md for details |

## 3. Key Data Flows

### 3.1 Discovery and Adoption (from Unifi Protect's perspective)

```
Protect ──UDP Probe──▶ WS-Discovery ──ProbeMatches(XAddrs=http://ip:port/onvif/device_service)──▶ Protect
Protect ──GetSystemDateAndTime (unauthenticated, time sync)──▶ Device Service
Protect ──GetCapabilities / GetServices (with WSSE)──▶ Device Service (returns Media XAddr)
Protect ──GetProfiles / GetStreamUri──▶ Media Service (returns rtsp://ip:rtspProxyPort/<path>)
Protect ──RTSP DESCRIBE/SETUP/PLAY──▶ RTSP Proxy ──pass-through──▶ real camera
```

### 3.2 Snapshot

```
Client ──GetSnapshotUri──▶ Media Service (returns http://ip:soapPort/onvif/snapshot?token=<profile>)
Client ──GET /onvif/snapshot──▶ ffmpeg -i <real rtsp> -frames:v 1 ──▶ JPEG
```

Snapshot results are cached in memory with a short TTL (10s by default), to avoid ffmpeg storms caused by client polling.

### 3.3 Config Hot Reload

```
UI save ──PUT /api/config──▶ validation (YAML syntax + port conflicts + field validity)
  ├─ validation fails → 400 + error detail, nothing written to disk
  └─ validation succeeds → atomically write config.yaml → Manager.Reload()
                  └─ stop old Virtual Devices → start new Virtual Devices → WS-Discovery updates device table (Bye/Hello)
```

### 3.4 MCP Tool Invocation

```
AI client (e.g. Claude Code) ──POST /mcp (JSON-RPC)──▶ go-sdk (Streamable HTTP, stateless)
  └─ routes to one of 12 tools (list_devices/add_device/probe_rtsp/take_snapshot/run_onvif_selftest, etc.)
       └─ reuses the same backend logic as the REST API (Backend/Config Store) ──▶ result (text/image blocks) ──▶ AI client
```

See docs/07-mcp.md for the detailed tool list, authentication, and error semantics.

## 4. Directory Layout (planned)

```
onvif-proxy/
├── cmd/onvif-proxy/main.go        # entry point: flag parsing, wiring, signal handling
├── internal/
│   ├── config/                    # YAML model, load/validate/atomic write, default value generation (UUID/MAC)
│   │   ├── env.go                 # ApplyEnvOverrides: ONVIF_* environment variable runtime overrides (in-memory only, see docs/03 §3)
│   │   └── env_test.go
│   ├── soap/                      # Envelope parsing, WSSE validation, Fault/response rendering
│   ├── onvif/
│   │   ├── device.go              # Device Service handlers
│   │   ├── media.go               # Media Service handlers
│   │   ├── templates/             # XML response templates (text/template)
│   │   └── server.go              # per-device HTTP server, routing, snapshot endpoint
│   ├── discovery/                 # WS-Discovery UDP listener and message handling
│   ├── rtspproxy/                 # TCP pass-through proxy
│   ├── rtsp/                      # native RTSP probe client (OPTIONS/DESCRIBE, Digest, SDP parsing)
│   ├── mediautil/                 # ffmpeg snapshot / MJPEG preview wrapper
│   └── web/
│       ├── web.go / devices.go …  # REST API
│       ├── mcp.go                 # /mcp endpoint: registers 12 MCP tools, reuses REST backend logic (see docs/07-mcp.md)
│       ├── mcp_test.go
│       ├── onvif_selftest.go      # ONVIF self-test: calls each method against the local virtual device and compares against the baseline table
│       ├── test.go                # RTSP probe/connectivity test etc. /api/test/* endpoints
│       ├── ui/                     # Preact+TSX source (package.json / tsconfig / src), built with esbuild
│       └── static/                # go:embed target: index.html shell + dist/ (committed build output)
├── docs/                          # this design documentation
├── config.example.yaml
├── Dockerfile
├── compose.yaml
└── README.md
```

## 5. Technology Choices and Rationale

| Decision | Choice | Rationale |
|------|------|------|
| SOAP implementation | **Hand-written XML templates**, no WSDL code generation | ONVIF-WSDL-generated Go code is bulky and hard to control; the upstream project's 500 issue stems precisely from its SOAP library's crude handling of unregistered methods. Hand-written templates can be diffed byte-for-byte against real camera messages, and Fault semantics stay fully under our control |
| XML parsing | standard library `encoding/xml` (token-stream style) | Only needs to extract the first child element name of the Body, a handful of parameters, and the WSSE header — no need for full deserialization |
| Config | `gopkg.in/yaml.v3` | The sole third-party dependency on the core path; support for comment-preserving scenarios can be evaluated later |
| MCP endpoint | official `github.com/modelcontextprotocol/go-sdk` | The single exception to the dependency-minimization constraint (see docs/07): the MCP protocol surface (lifecycle/session/SSE/version negotiation) is maintained long-term by the official library, and reinventing it has low payoff; used only by the `/mcp` route in `internal/web` |
| UUID/MAC | self-generated via `crypto/rand` (RFC 4122 v4 / locally administered MAC) | No dependency required; persisted back to YAML after first generation, keeping the device identity stable in the client's eyes |
| Snapshot/preview | external ffmpeg process | Implementing stream pulling/decoding in-house would be too costly; ffmpeg is bundled in the Docker image, and running on the host requires it to be present on PATH |
| RTSP probing | **native implementation** (not via ffmpeg) | The UI's "test connection" needs to precisely distinguish error categories (TCP unreachable / 401 auth failure / 404 wrong path / SDP has no video track); ffmpeg's error output cannot be parsed programmatically |
| Web UI | **Preact + TSX + esbuild**, packaged via `go:embed` | Componentization + TypeScript type safety (device cards/forms/config editor are more maintainable than single-file vanilla JS); the Preact runtime is only ~6KB. The build output (`internal/web/static/dist/{app.js,app.css}`) **is committed to the repository**, baked into the binary via `go:embed all:static`; **zero Node dependency** at runtime/in the Docker image, keeping the single-binary property intact. Source lives in `internal/web/ui/` (`npm run build` bundles via esbuild; CI verifies dist has not drifted) |

## 6. Concurrency and Lifecycle

- Each Virtual Device's HTTP server and RTSP proxy run in their own goroutine; `Manager` uses a `context` tree for unified cancellation.
- Hot reload semantics: **stop-then-start** (stop everything, then start everything). With a small number of devices (single digits to a few dozen), the interruption is < 1s, trading that off for a simpler implementation with no port-occupancy race.
- ffmpeg child process lifetime is bound to the HTTP request lifetime: client disconnects → context cancelled → process group killed, leaving no zombies.
- Graceful shutdown: SIGTERM → WS-Discovery sends Bye → all listeners closed → in-flight requests awaited (5s timeout).

## 7. Non-Goals (explicitly out of scope)

- PTZ, audio backchannel, Events/doorbell, ONVIF Profile T/G/M — Profile S live streaming + snapshot already satisfies Unifi Protect adoption requirements.
- RTSP re-muxing/transcoding — the stream the client receives is exactly the real camera's stream; whether H.265 works is up to the client itself.
- Multi-node/cluster — single process, single config file.
