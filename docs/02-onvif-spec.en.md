# 02 · ONVIF Spec Conformance

Concrete points where this project "does things properly per spec." Specifications referenced:

| Specification | Purpose |
|------|------|
| ONVIF Core Specification | Device service, discovery, security, Fault semantics |
| ONVIF Media Service Specification (ver10) | Media service, Profile/StreamUri/SnapshotUri |
| ONVIF Profile S | Baseline for judging the mandatory feature set |
| W3C SOAP 1.2 | Envelope structure, Fault structure, HTTP status code mapping |
| WS-Discovery (2005/04 draft, the version ONVIF specifies) | UDP 3702 multicast discovery |
| OASIS WS-Security UsernameToken Profile 1.0 | WSSE PasswordDigest authentication |
| RFC 2326 (RTSP 1.0) / RFC 7826 (RTSP 2.0) | RTSP probe client (implemented against 1.0, compatible with the vast majority of cameras) |
| RFC 7616 / RFC 2617 (HTTP Digest) | 401 authentication for RTSP probing |
| RFC 4566 (SDP) | Parsing DESCRIBE results |
| RFC 4122 (UUID) | Device EndpointReference address |

## 1. SOAP Layer Specification

### 1.1 Request Handling

- Only `POST` is accepted; `Content-Type` accepts both `application/soap+xml` and `text/xml` (to accommodate SOAP 1.1 client conventions, while the message is processed per 1.2).
- Method routing is based on **the localName + namespace of the Body's first child element**, not on the SOAPAction/WS-Addressing Action header (some clients don't send it).
- Extraction uses `encoding/xml` token streaming rather than full deserialization; parameters (e.g. `ProfileToken`) are extracted on demand.

### 1.2 Response

- `Content-Type: application/soap+xml; charset=utf-8` uniformly.
- Response templates are hand-written element by element against the ONVIF WSDL definitions, with namespace prefixes fixed and declared on the Envelope (`tds`/`trt`/`tt`/`ter`, etc.).
- All dynamic fields are XML-escaped.

### 1.3 Fault Semantics (the key differentiator)

Problem with upstream projects: an unregistered method causes the soap library to throw a non-conformant 500. This project implements the fault table per the ONVIF Core Spec:

| Scenario | SOAP Code/Subcode | HTTP Status |
|------|-------------------|-------------|
| Method not implemented (optional method) | `env:Receiver` / `ter:ActionNotSupported` | 500 (SOAP 1.2 mandates Receiver→500; **the body is a conformant Fault XML**, so clients can degrade gracefully) |
| Malformed request / missing parameter | `env:Sender` / `ter:InvalidArgVal` | 400 |
| Unauthenticated / authentication failed | `env:Sender` / `ter:NotAuthorized` | 400 |
| Unknown Profile token | `env:Sender` / `ter:InvalidArgVal` | 400 |

Fault message structure (SOAP 1.2 §5.4): `Code/Subcode/Reason/Detail` all present, `Reason` carries `xml:lang="en"`.

## 2. Authentication (WSSE)

- Implements **PasswordDigest** verification per the OASIS UsernameToken Profile:
  `Digest = Base64( SHA-1( Base64Decode(Nonce) + Created + Password ) )`
- `Created` timestamp tolerance is ±5 minutes (a compile-time hardcoded constant, not currently configurable); replay-protection nonce caching is deliberately skipped for now (LAN scenario, complexity trade-off).
- **`GetSystemDateAndTime` is always unauthenticated** (the Core Spec explicitly requires this: the client needs it to sync its clock before it can compute a valid Digest).
- Per-device `auth.username/password` is configurable; if not configured, **everything is allowed through** (any/missing WSSE header is accepted) — consistent with upstream behavior, for convenience on internal networks.
- PasswordText mode is accepted as well (some clients only send plaintext).

## 3. WS-Discovery

- A single UDP socket joins the multicast group `239.255.255.250:3702` and answers on behalf of all virtual devices within this process.
- **Hello**: multicast-sent on each device's startup/hot-reload; **Bye**: sent on graceful shutdown/device removal.
- **Probe → ProbeMatches**:
  - Check `d:Types`: respond only if empty or containing `dn:NetworkVideoTransmitter`/`tds:Device`;
  - Reply unicast after a random delay within `0 ~ APP_MAX_DELAY(500ms)` per spec, with one ProbeMatch per device for multiple devices;
  - `RelatesTo` echoes back the request's `MessageID`; `XAddrs` is `http://<ip>:<port>/onvif/device_service`.
- Scopes (also used for `GetScopes`):
  ```
  onvif://www.onvif.org/type/video_encoder
  onvif://www.onvif.org/type/Network_Video_Transmitter
  onvif://www.onvif.org/Profile/Streaming
  onvif://www.onvif.org/name/<device name>
  onvif://www.onvif.org/hardware/<model>
  onvif://www.onvif.org/location/
  ```
- Device EPR address: `urn:uuid:<device uuid>`; the UUID is persisted to YAML after first generation, so the client still recognizes the same device after a restart.

## 4. Device Service Method List

Path `POST /onvif/device_service`. ✅ = fully implemented, ◽ = empty/fixed-value response (valid but empty), ✗ = conformant Fault.

| Method | Status | Notes |
|------|:---:|------|
| GetSystemDateAndTime | ✅ | UTC + local time, `DateTimeType=NTP`; unauthenticated |
| SetSystemDateAndTime | ◽ | Accepted, returns an empty response (a virtual device has no system clock to set) |
| GetCapabilities | ✅ | Device/Media sections; Media includes `StreamingCapabilities(RTP_TCP, RTP_RTSP_TCP)`, snapshot; Events/PTZ/Analytics are not declared |
| GetServices | ✅ | Device + Media entries, with version numbers; `IncludeCapability` supported |
| GetServiceCapabilities | ✅ | Minimal valid set for Network/System/Security |
| GetDeviceInformation | ✅ | Manufacturer/Model/FirmwareVersion/SerialNumber/HardwareId, from config (with defaults) |
| GetScopes | ✅ | See the Scopes list above (one of the methods that 500s upstream) |
| SetScopes / AddScopes | ✗ | ActionNotSupported |
| GetNetworkInterfaces | ✅ | Single interface `eth0`: HwAddress = configured MAC, IPv4 manual address + prefix (one of the methods that 500s upstream) |
| GetNetworkProtocols | ✅ | HTTP (SOAP port) + RTSP (proxy port) |
| GetHostname | ✅ | FromDHCP=false, Name = device name slug |
| GetDNS / GetNTP | ◽ | Minimal valid response with FromDHCP=true |
| GetUsers | ✅ | If auth is configured → returns that user (Administrator); if not configured → empty list |
| CreateUsers/DeleteUsers/SetUser | ✗ | ActionNotSupported |
| GetWsdlUrl | ✅ | `http://www.onvif.org/` |
| SystemReboot | ◽ | Returns `Message: Rebooting...`, actually a no-op |
| GetSystemLog / GetSystemBackup | ✗ | ActionNotSupported |
| All other methods not listed | ✗ | ActionNotSupported (conformant Fault) |

## 5. Media Service Method List

Path `POST /onvif/media_service` (truthfully advertised in `GetCapabilities`/`GetServices`).

Data model (**multi-Profile**, count determined by the configured `streams[]`, N ≥ 1): per device, 1 VideoSource (token `src`), 1 VideoSourceConfiguration (token `vsc`, shared across all Profiles), and per stream, 1 VideoEncoderConfiguration (token `vec_<name>`) and 1 Profile (token `profile_<name>`, `fixed=true`). ONVIF allows attaching any number of Profiles to the same VideoSource — this is spec-compliant; Unifi Protect picks the first two as the high/low streams, while other clients (Frigate, Home Assistant) can pick any Profile.

| Method | Status | Notes |
|------|:---:|------|
| GetServiceCapabilities | ✅ | `SnapshotUri`/`StreamingCapabilities(RTP_TCP, RTP_RTSP_TCP)`/`ProfileCapabilities(MaximumNumberOfProfiles=stream count)` |
| GetProfiles / GetProfile | ✅ | One Profile per configured stream (N total); includes full VSC + VEC nodes |
| CreateProfile / DeleteProfile | ✗ | ActionNotSupported (Profiles are fixed) |
| GetVideoSources | ✅ | Resolution/frame rate taken from the primary stream's (streams[0]) configuration |
| GetVideoSourceConfigurations / ~Configuration | ✅ | Bounds = full frame |
| GetVideoEncoderConfigurations / ~Configuration | ✅ | H264 (H265 pass-through declaration configurable), resolution/frame rate/bitrate/GOP per stream from config |
| GetVideoEncoderConfigurationOptions | ✅ | Only lists the resolution tiers of each configured stream (a virtual device can't actually change the encoding) |
| SetVideoEncoderConfiguration | ◽ | Accepted and ignored (returns an empty success; onboarding flows on clients often call this) |
| GetStreamUri | ✅ | `rtsp://<ip>:<that stream's proxy port>/<original path>`; looks up the corresponding stream by ProfileToken |
| GetSnapshotUri | ✅ | `http://<ip>:<soapPort>/onvif/snapshot?token=<profile>`; two backend modes: pass-through of the real camera's snapshot URL, or ffmpeg frame capture (with TTL cache), see the `snapshot` section of doc 03 |
| GetGuaranteedNumberOfVideoEncoderInstances | ✅ | `TotalNumber`/`H264` both equal the configured stream count |
| GetAudioSources / GetAudioEncoderConfigurations and other audio-related methods | ◽ | Empty list (a valid response declaring no audio) |
| GetCompatibleVideoSourceConfigurations / GetCompatibleVideoEncoderConfigurations and other Compatible-type methods | ✅ | Share the rendering function with the corresponding Get~Configuration(s), returning the same fixed configuration |
| GetOSDs | ◽ | Empty list |
| All other methods not listed | ✗ | ActionNotSupported |

## 6. RTSP Probe Client (Web UI "Test Connection")

A native implementation (no ffmpeg dependency), per RFC 2326:

1. TCP connect (5s timeout) → `OPTIONS` (with `CSeq`, `User-Agent`);
2. `DESCRIBE` (`Accept: application/sdp`); on 401, supports both **Digest (RFC 7616, MD5)** and **Basic** in turn, per `WWW-Authenticate`;
3. Parse the SDP (RFC 4566): `m=video/audio` tracks, `a=rtpmap` (codec name H264/H265/…), H264 `sprop-parameter-sets` in `a=fmtp` can be decoded for profile-level, `a=framerate`/`a=framesize` (if present);
4. Output a structured result: `connectivity / auth result / status code / codec / track count / server header`; the UI displays errors by category (TCP unreachable ≠ wrong password ≠ path 404).

## 7. Acceptance Baseline vs. Upstream

Once implementation is complete, the self-check (Web UI "ONVIF Self-Check" button, see doc 04) must be all-green:

| Method | p10tyr image | daniela-hase upstream | This project's target |
|------|:---:|:---:|:---:|
| GetCapabilities | ❌ 500 | ✅ | ✅ 200 |
| GetServices | ✅ | ✅ | ✅ 200 |
| GetSystemDateAndTime | ✅ | ✅ | ✅ 200 (unauthenticated) |
| GetScopes | ❌ 500 | ❌ | ✅ 200 |
| GetNetworkInterfaces | ❌ 500 | ❌ | ✅ 200 |
| GetDeviceInformation | — | ✅ | ✅ 200 |
| GetProfiles / GetStreamUri / GetSnapshotUri | — | Partial | ✅ 200 |
| Any unimplemented method | Bare 500 | Bare 500 | Conformant ActionNotSupported Fault |
