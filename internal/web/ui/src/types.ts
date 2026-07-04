// Wire types mirroring the JSON shapes served by internal/web (docs/04).

export interface StreamView {
  name: string;
  profile_token: string;
  rtsp_uri: string;
  rtsp: string;
  width: number;
  height: number;
  framerate: number;
  bitrate: number;
}

export interface DeviceView {
  name: string;
  uuid: string;
  soap_port: number;
  rtsp_port: number;
  running: boolean;
  auth_user: string;
  endpoints: {
    device_service: string;
    snapshot: string;
    streams: StreamView[];
  };
}

export interface StatusView {
  version: string;
  advertise_ip: string;
  uptime_seconds: number;
  ffmpeg: boolean;
}

export interface RTSPTrack {
  type: string;
  codec: string;
  fmtp: string;
}

export interface RTSPResult {
  ok: boolean;
  status: number;
  auth: string;
  server: string;
  latency_ms: number;
  tracks: RTSPTrack[];
  err_kind: string;
  err_detail: string;
}

export interface StreamInfo {
  codec: string;
  width: number;
  height: number;
  fps: number;
  /** kbps, measured or source-declared; 0 = unknown */
  bitrate: number;
}

export interface OnvifCheck {
  method: string;
  http_status: number;
  soap_fault: string;
  pass: boolean;
}

// Request body accepted by POST /api/devices and PUT /api/devices/{uuid}.
export interface DeviceSpec {
  name: string;
  soap_port: number;
  rtsp_port: number;
  auth?: { username: string; password: string };
  streams: Array<{
    name: string;
    rtsp: string;
    width: number;
    height: number;
    framerate: number;
    bitrate: number;
  }>;
}
