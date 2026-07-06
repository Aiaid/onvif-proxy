# 03 · Configuration File Format

The configuration file is YAML, with a default path of `./config.yaml` (overridable via the `-config` flag or the `CONFIG` environment variable). Edits made through the Web UI are validated and then **atomically written back** to the same file; auto-generated persistent fields (uuid, mac) are also written back. A small set of global fields support runtime overrides via environment variables (see Section 3), which makes it easy to change config in Docker deployments without mounting a file.

## 1. Full example

```yaml
# Global
server:
  # The local IP advertised to clients. If left empty, it is auto-detected
  # (using the default route's outbound address).
  # In Docker bridge mode this must be set explicitly to the host IP; in macvlan mode, to the container's own IP.
  advertise_ip: ""
  # WS-Discovery switch (turn off if you only want to add devices manually in some environments)
  discovery: true

web:
  enabled: true
  port: 8080
  # HTTP Basic auth for the Web UI; leave empty to disable
  username: ""
  password: ""

# Virtual device list — each entry = one ONVIF camera
devices:
  - name: "Garage Camera"           # Required; used for the ONVIF name scope and UI display
    # ---- The following three fields are auto-generated and written back when left empty, keeping device identity stable ----
    uuid: ""                     # RFC 4122 v4
    mac: ""                      # Locally administered address (x2-xx-xx-xx-xx-xx)
    serial: ""                   # SerialNumber; defaults to the first 8 characters of the uuid

    ports:
      soap: 8081                 # ONVIF SOAP/HTTP port (includes the snapshot endpoint)
      rtsp: 8554                 # RTSP passthrough proxy port

    info:                        # GetDeviceInformation fields, all optional
      manufacturer: "OnvifProxy"
      model: "VirtualCam"
      firmware: "1.0.0"

    auth:                        # ONVIF WSSE auth; the whole block can be omitted (omitted = no auth required)
      username: "admin"
      password: "secret"

    # Stream list → one ONVIF Profile per stream (unlimited count, at least 1 required).
    # The first entry is the primary stream: the VideoSource resolution and default snapshot source are taken from it.
    streams:
      - name: main               # profile token = profile_main; unique within the device
        rtsp: "rtsp://user:pass@192.168.1.50:554/h264/ch1/main/av_stream"
        width: 2560
        height: 1440
        framerate: 25
        bitrate: 4096            # kbps, used only for capability advertisement
      - name: sub
        rtsp: "rtsp://user:pass@192.168.1.50:554/h264/ch1/sub/av_stream"
        width: 640
        height: 360
        framerate: 15
        bitrate: 512
      - name: mobile             # A third, fourth entry, etc. can be added freely
        rtsp: "rtsp://user:pass@192.168.1.50:554/h264/ch1/mobile/av_stream"
        width: 352
        height: 288
        framerate: 10
        bitrate: 128

    snapshot:
      # Optional. If the real camera has its own HTTP snapshot endpoint, fill in its URL here and the proxy will
      # forward it directly (passthrough mode); if left empty, ffmpeg grabs a frame from the stream
      # (defaults to the first stream; can be specified via `stream`).
      url: ""
      stream: ""                 # Which stream ffmpeg should grab a frame from; defaults to streams[0] (the primary stream has the highest resolution but grabbing is slightly slower; specifying sub is faster)
      cache_seconds: 10

  - name: "Front Door NVR Channel 3"
    ports: { soap: 8082, rtsp: 8555 }
    streams:
      - name: main
        rtsp: "rtsp://admin:pw@192.168.1.60:554/cam/realmonitor?channel=3&subtype=0"
        width: 1920
        height: 1080
        framerate: 20
        bitrate: 2048
```

## 2. Field reference and validation rules

### server

| Field | Type | Default | Description |
|------|------|------|------|
| `advertise_ip` | string | auto-detected | The IP written into XAddrs / StreamUri / SnapshotUri. **URIs inside HTTP responses preferentially use the request's Host header** (NAT-friendly); this field is mainly used for WS-Discovery |
| `discovery` | bool | true | Whether to start UDP 3702 |

### devices[]

| Field | Required | Validation |
|------|:---:|------|
| `name` | ✅ | Non-empty; URL-encoded when entering the scope |
| `uuid` / `mac` / `serial` | — | Generated and **written back to the file** if empty; uuid must be a valid UUID, mac must be a valid MAC address |
| `ports.soap` / `ports.rtsp` | ✅ | 1-65535; **no conflicts across all devices plus the web port** (validation failure rejects load/save) |
| `streams[]` | ✅ | At least 1 entry; each → one ONVIF Profile. `name` must be unique within the device (lowercase letters/digits/underscore, used in tokens: `profile_<name>` / `vec_<name>`); the first entry is the primary stream |
| `streams[].rtsp` | ✅ | Must use the `rtsp://` scheme; may include userinfo; host and path are parsed for use as the proxy target and StreamUri path |
| `streams[].width/height/framerate/bitrate` | ✅ | Positive integers; used only for ONVIF capability advertisement, does not affect the actual stream |
| `streams[].proxy_port` | — | **Required** when this stream's upstream host:port differs from the primary stream (needs its own listening proxy); omit if it matches, in which case it shares `ports.rtsp` |
| `auth` | — | If provided, both username and password must be non-empty. **This is a layer independent from RTSP credentials**: `auth` protects the virtual device's ONVIF interface (WSSE), while RTSP authentication is done end-to-end between the client and the real camera. Omitting it = no auth required (recommended); if configured, it's suggested to match the camera's RTSP credentials (Unifi Protect only accepts one set of credentials and uses it for both layers) |
| `snapshot.url` | — | If set, uses passthrough mode; must use the `http(s)://` scheme |
| `snapshot.stream` | — | The stream name used for frame grabbing in ffmpeg mode; defaults to `streams[0].name` |

### RTSP URL breakdown logic

`rtsp://user:pass@host:port/path?query` is split into:

- **Proxy target**: `host:port` (the peer for TCP passthrough);
- **StreamUri return value**: `rtsp://<advertise>:<proxy port>/path?query` — **credentials do not appear in the StreamUri**; RTSP authentication is done end-to-end between the client and the real camera (the camera credentials entered in the client = the real camera's credentials);
- **Proxy port allocation for multiple streams**: streams sharing the same `host:port` as the primary stream share `ports.rtsp`; streams with a different upstream must explicitly specify `proxy_port` (one listening port per distinct upstream). The validator checks that all (soap/rtsp/proxy_port/web) ports have no global conflicts.

### Port planning suggestions

| Purpose | Suggested range |
|------|----------|
| web | 8080 |
| device soap | 8081, 8082, … |
| device rtsp | 8554, 8555, … |

## 3. Environment variable overrides

Aimed at Docker/compose scenarios: global fields in the `server` and `web` sections can be overridden via environment variables, avoiding the need to pre-write sensitive information into a mounted volume.

| Environment variable | Overridden field | Value |
|------|------|------|
| `CONFIG` | Config file path (equivalent to the `-config` flag) | Path |
| `ONVIF_ADVERTISE_IP` | `server.advertise_ip` | IP string |
| `ONVIF_DISCOVERY` | `server.discovery` | `true` / `false` |
| `ONVIF_WEB_ENABLED` | `web.enabled` | `true` / `false` |
| `ONVIF_WEB_PORT` | `web.port` | 1-65535 |
| `ONVIF_WEB_USERNAME` | `web.username` | String |
| `ONVIF_WEB_PASSWORD` | `web.password` | String |

Semantics:

- **Precedence: env > yaml**; overrides take effect only in the process's memory and are **never written back** to config.yaml — device add/edit/delete and whole-config saves from the Web UI all start from the original file contents, so env values never get baked into the mounted config file;
- Unset or an empty string = no override (falls back to the yaml value / default);
- After overriding, full validation is re-run (global port conflict check, username/password must be provided as a pair); an invalid value or validation failure causes **startup to fail**, with the reason printed;
- The condition for enabling Basic auth is unchanged: it's enabled only if the effective `username` is non-empty. So if yaml has an empty username and only `ONVIF_WEB_PASSWORD` is set, validation will fail due to the pairing constraint — `ONVIF_WEB_USERNAME` must be set at the same time;
- The device list (`devices`) does not support env overrides — configure per-device settings via the mounted config.yaml or the Web UI.

## 4. Ways to generate a configuration

1. **Zero-config startup (implemented)**: if the config file doesn't exist, a default configuration is auto-generated (web enabled, devices empty), and devices are then added via the Web UI;
2. **Web UI (implemented)**: "➕ Add Device" form → fill in the primary/sub-stream RTSP URLs → click "Probe" to test connectivity first, then auto-fill codec/resolution/framerate → fill in the name and ports (defaults suggest the next free port pair), optional ONVIF auth → "Add" merges into the YAML via `POST /api/devices` and hot-reloads; the device card's "Delete" button removes it via `DELETE /api/devices/{uuid}`;
3. Hand-write the YAML (copy from `config.example.yaml`).
