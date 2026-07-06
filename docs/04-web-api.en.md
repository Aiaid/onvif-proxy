# 04 · Web Backend API & UI Design

The web service listens on `:8080` by default, providing a REST API and an embedded single-page UI. Optional HTTP Basic authentication (`web.username/password`).

## 1. REST API

General conventions: JSON responses; error format `{"error": "...", "detail": "..."}`; write operations use `PUT`/`POST` only.

### 1.1 Configuration management

| Method/Path | Description |
|-----------|------|
| `GET /api/config` | Returns the raw current config.yaml contents (`text/plain`), displayed directly by the UI editor |
| `PUT /api/config` | Body = full YAML text. Flow: parse → validate (syntax/port conflicts/fields) → atomic write to disk → hot reload. On validation failure, returns 400 with itemized errors, **nothing is written to disk** |
| `GET /api/devices` | Structured device list + runtime status: `{name, uuid, soap_port, rtsp_port, running, auth_user, endpoints:{device_service, snapshot, streams:[{name, profile_token, rtsp_uri, rtsp, width, height, framerate, bitrate}]}}` (the `streams` array corresponds one-to-one with the configured multi-profile streams). `rtsp_port`/`auth_user` and each stream's `rtsp` (the upstream source URL, may contain credentials), `width/height/framerate/bitrate` are extra fields added to **pre-fill the edit form**: the exposure policy for source URL credentials matches `GET /api/config` (returns the full text verbatim) — both are gated by web Basic auth; the ONVIF password is **not** sent down (see the password-retention semantics of PUT below) |
| `POST /api/devices` | Body is a JSON DeviceSpec `{name, soap_port, rtsp_port, auth?, streams:[{name,rtsp,width,height,framerate,bitrate}], snapshot?}`. Merged into the current YAML → validate → write to disk → hot reload; uuid/mac/serial are generated at load time. On validation failure (including port conflicts) returns 400; on success `{"status":"applied"}` |
| `PUT /api/devices/{uuid}` | Body is the same DeviceSpec as POST. Locates the device by uuid (unknown → 404) → builds a new Device from the form values, **replacing it in place**, but **retains the original uuid/mac/serial and info** (the form does not carry these) → validate → write to disk → hot reload. On success `{"status":"applied"}`, validation/port-conflict errors pass through as 400. **ONVIF password retention semantics**: since the DTO only exposes `auth_user` and never the password, if the body carries `auth.username` but `auth.password` is empty, this is treated as "keep the existing password unchanged"; clearing the username (omitting the `auth` object) removes ONVIF authentication |
| `DELETE /api/devices/{uuid}` | Deletes the device by uuid from the current config → validate → write to disk → hot reload. Unknown uuid returns 404, success `{"status":"applied"}` |

### 1.2 Test tools (corresponding to the UI test panel)

| Method/Path | Description |
|-----------|------|
| `POST /api/test/rtsp` | Body `{"url": "rtsp://..."}`. Native RTSP probe (OPTIONS + DESCRIBE + Digest/Basic auth), returns: `{"ok":true, "status":200, "auth":"digest", "server":"...", "tracks":[{"type":"video","codec":"H264","fmtp":"..."}], "latency_ms": 43}`. Error categories: `dial_timeout` / `auth_failed` / `not_found` / `no_video_track` / `protocol_error` |
| `POST /api/test/streaminfo` | Body `{"url": "rtsp://..."}`. Probes the first video stream via ffprobe, returns `{"codec":"h264","width":1920,"height":1080,"fps":25,"bitrate":2048}`, used to pre-fill the add-device form. `bitrate` (kbps): if the source declares it, that value is used; otherwise ffmpeg pulls the stream for 3 seconds and measures the actual byte rate; if it cannot be measured, 0 (the form keeps the manually entered value). Returns 501 if ffmpeg is unavailable |
| `GET /api/test/snapshot?device=<uuid>&stream=<name>` | Calls ffmpeg to grab one frame from the specified stream (`stream` omitted → main stream), returns `image/jpeg`. On failure returns a JSON error (including an ffmpeg stderr summary) |
| `GET /api/preview?device=<uuid>&stream=<name>` | **MJPEG live preview**: ffmpeg pulls the specified stream → `-f mpjpeg` (scaled to 640 wide, 5fps, muted) → pushed to the browser as `multipart/x-mixed-replace`, played directly by an `<img>`. Disconnecting the client immediately kills ffmpeg; each device has a cap of 2 concurrent previews |
| `POST /api/test/onvif?device=<uuid>` | **ONVIF self-check**: the server acts as an ONVIF client calling its own SOAP endpoint, returning `{method, http_status, soap_fault, pass}` per method. Covered methods: GetSystemDateAndTime, GetCapabilities, GetServices, GetScopes, GetNetworkInterfaces, GetDeviceInformation, GetProfiles, GetStreamUri, GetSnapshotUri, plus one deliberately nonexistent method (to verify Fault compliance) |
| `GET /api/discovery/log` | The most recent 50 WS-Discovery interactions (who sent a Probe, what was returned), used to troubleshoot "client can't discover the device" issues |

### 1.3 System

| Method/Path | Description |
|-----------|------|
| `GET /api/status` | Version, uptime, whether ffmpeg is available, advertise_ip probe result |
| `GET /healthz` | Liveness check, 200 is sufficient |

### 1.4 MCP endpoint

`/mcp` (Streamable HTTP, JSON-RPC, non-REST style) exposes management capabilities as MCP
tools for AI clients; it shares the same port and the same Basic auth as the REST API. See
`docs/07-mcp.en.md` for the protocol, tool list, and implementation contract.

## 2. UI page design (single page, three sections)

**Tech stack**: Preact + TSX, bundled by esbuild into `static/dist/{app.js,app.css}` (committed to the repo, embedded into the binary via `go:embed`, zero Node needed at runtime). Source lives in `internal/web/ui/`, `index.html` is a thin shell (`<div id="app">` + `/dist/app.js`). Development: `cd internal/web/ui && npm install && npm run check && npm run build`.

**Robustness convention (global)**: all async operations go through the unified `useAsync` + `AsyncButton` mechanism — the button **immediately disables and shows a spinner label** on click, and restores once the request completes/fails; `useAsync` has a built-in re-entrancy guard, which naturally prevents double-clicks/duplicate form submits; for delete-type operations, the button locks as soon as `confirm()` is accepted. `fetch` is uniformly wrapped (`api.ts`): 20s timeout (AbortController), non-2xx throws `ApiError`, and parses the `{error,detail}` error envelope. There is no bare async `onClick`.

### 2.1 Device list (home page)

Each device is a card:

```
┌────────────────────────────────────────────────┐
│ Garage Camera                   ● Running       │
│ ONVIF: http://192.168.1.10:8081/onvif/device_service
│ RTSP:  rtsp://192.168.1.10:8554/h264/ch1/main…  │
│ [Test Connection] [Snapshot] [Preview] [ONVIF Self-check] [Edit] [Delete] │
└────────────────────────────────────────────────┘
```

- **Test Connection** → `/api/test/rtsp`, shows the result in the card: connectivity/auth/codec/latency, errors are shown as localized hints by category ("TCP unreachable, check IP and port" / "Authentication failed, check username and password" / "Path 404");
- **Snapshot** → shows the JPEG directly in the card;
- **Preview** → an overlay with `<img src=/api/preview…>` live playback, closing it (clicking the backdrop/button/Esc) clears `src`, tearing down the stream and killing ffmpeg;
- **ONVIF Self-check** → renders a method × status table (green checkmark/red X), an automated version of the comparison table that originally motivated this project;
- **Edit** → opens the **edit mode** of the same form component used for "Add Device", pre-filled using the extra fields returned by `GET /api/devices` (soap/rtsp ports, `auth_user`, each stream's `rtsp/width/height/framerate/bitrate`), and submits via `PUT /api/devices/{uuid}`; leaving the ONVIF password field blank keeps the existing password;
- **Delete** → `DELETE /api/devices/{uuid}` after `confirm`, refreshing the device list and config editor on success.

### 2.2 Config editor

- Full-text YAML editor (textarea + monospace font, no frontend dependency introduced);
- "Validate" button: dry-run call to `PUT /api/config?dry_run=1`, reports errors only, nothing is written;
- "Save & Apply" button: writes to disk + hot reload, shows the reload result;
- Top notice bar: config file path, last save time.

### 2.3 Add Device wizard (form)

1. Enter the RTSP URL → click "Probe" → automatically fetches codec/resolution (pre-filled from SDP if available, otherwise entered manually);
2. Enter name, ports (automatically suggests the next free port pair), optional low-bitrate substream;
3. "Add" → the backend merges the device node into the YAML → validate → write to disk → hot reload.

## 3. Security considerations

- The Web UI is intended for internal-network administrators; once Basic auth is enabled, all `/api/*` routes and static pages require authentication;
- `/api/test/rtsp` only accepts the `rtsp://` scheme, and is forbidden from being used as a pretext to probe arbitrary TCP ports with other protocols (SSRF surface reduction: the target port is unrestricted, but the protocol handshake must be RTSP);
- Camera passwords in the config file: `GET /api/config` returns them verbatim (the editor needs to be able to modify them), so **enabling web authentication is a prerequisite for using preset passwords** — this is called out in both the documentation and the UI;
- ffmpeg command-line arguments are all assembled programmatically; the RTSP URL is passed via `exec.Command` arguments, leaving no shell-injection surface.
