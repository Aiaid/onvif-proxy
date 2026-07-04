// Bilingual (Simplified Chinese / English) dictionary and language plumbing for
// the UI. Every user-visible string lives here under a semantic camelCase key.
//
// Typing: `zh` is the canonical shape; `type Dict = typeof zh` derives it, and
// `en` is annotated `: Dict`, so a missing or misspelled key in `en` is a
// compile error and the two languages can never drift apart in key set.
//
// Strings that embed runtime values are function values (never string
// templating), so each language controls its own word order and punctuation.

import { createContext } from "preact";
import { useContext } from "preact/hooks";

export type LangKey = "zh" | "en";

const zh = {
  // Brand / document
  appTitle: "ONVIF Proxy",
  docTitle: "ONVIF Proxy 管理",

  // StatusBar
  statVersion: "版本",
  statFfmpeg: "ffmpeg",
  statFfmpegOk: "可用",
  statFfmpegNo: "不可用",

  // App / sections
  configHeading: "配置",
  devicesHeading: "设备",

  // DeviceList
  addDevice: "➕ 新增设备",
  loadDevicesFailed: (detail: string) => `加载设备失败: ${detail}`,
  loading: "加载中…",
  noDevices: "暂无设备。",

  // DeviceCard — status + actions
  deviceRunning: "运行中",
  deviceStopped: "未运行",
  btnTestConn: "测试连接",
  btnSnapshot: "快照",
  btnPreview: "预览",
  btnOnvifCheck: "ONVIF 自检",
  btnEdit: "编辑",
  btnDelete: "删除",
  busyProbing: "探测中…",
  busySnapshot: "抓取中…",
  busyOnvif: "自检中…",
  busyDelete: "删除中…",
  capturingSnapshot: "抓取快照…",
  confirmDelete: (name: string) => `确定删除设备“${name}”?此操作会立即热重载配置。`,

  // DeviceCard — RTSP probe result
  rtspResultLine: (status: number, auth: string, latencyMs: number) =>
    `连通 · 状态 ${status} · 认证 ${auth} · 延迟 ${latencyMs}ms`,
  rtspCodecLine: (codecs: string, server: string) => `编码: ${codecs} · Server: ${server}`,
  rtspProbeFailed: "探测失败。",
  hintDialTimeout: "TCP 连不通,检查 IP 与端口。",
  hintAuthFailed: "认证失败,检查用户名与密码。",
  hintNotFound: "路径 404,检查 RTSP 路径。",
  hintNoVideoTrack: "未发现视频轨,SDP 中无 video。",
  hintProtocolError: "RTSP 协议握手异常。",

  // DeviceCard — ONVIF self-check table
  thMethod: "方法",
  thResult: "结果",
  altSnapshot: "快照",

  // Shared error prefixes
  requestFailed: (detail: string) => `请求失败: ${detail}`,
  snapshotFailed: (detail: string) => `快照失败: ${detail}`,
  deleteFailed: (detail: string) => `删除失败: ${detail}`,

  // Preview overlay
  previewClose: "关闭",
  previewAlt: "实时预览",

  // DeviceModal — titles + fields
  titleAdd: "新增设备",
  titleEdit: "编辑设备",
  fieldDeviceName: "设备名称",
  phDeviceName: "车库摄像头",
  fieldStreamName: "名称",
  fieldRtspUrl: "RTSP URL",
  phRtspUrl: "rtsp://user:pass@host:554/path",
  btnProbe: "探测",
  btnRemove: "移除",
  fieldWidth: "宽",
  fieldHeight: "高",
  fieldFramerate: "帧率",
  fieldBitrate: "码率(kbps)",
  addSubStream: "+ 添加子码流",
  fieldSoapPort: "SOAP 端口",
  fieldRtspPort: "RTSP 端口",
  fieldOnvifUser: "ONVIF 用户名(可选)",
  fieldOnvifPass: "ONVIF 密码(可选)",
  phKeepPass: "留空 = 保持原密码",
  rtspAuthNote:
    "RTSP URL 中的账密(user:pass@)只供本代理抓快照/探测使用,不会下发给 ONVIF 客户端;客户端拉流时由真实摄像头直接认证,所以在 Unifi Protect 等客户端里收编时填的账密必须是摄像头本身的 RTSP 账密。",
  onvifAuthNote:
    "ONVIF 认证(WSSE)与上面的 RTSP 密码是独立的两层:这里保护的是虚拟设备的 ONVIF 接口。留空 = 不校验(推荐)。若要设置,建议与摄像头 RTSP 账密相同 —— Unifi Protect 只让填一组账密,并同时用于 ONVIF 与 RTSP 两层。",

  // DeviceModal — buttons
  btnCancel: "取消",
  btnSave: "保存",
  btnAdd: "添加",
  busySubmit: "提交中…",
  saveFailed: "保存失败",
  addFailed: "添加失败",

  // DeviceModal — client-side validation
  unnamedStream: "未命名",
  streamRowLabel: (index: number, name: string) => `流 ${index}(${name})`,
  vNameEmpty: "设备名称不能为空",
  vSoapPort: "SOAP 端口无效",
  vRtspPort: "RTSP 端口无效",
  vStreamName: (label: string) => `${label}:名称需为小写字母/数字/下划线`,
  vStreamRtsp: (label: string) => `${label}:RTSP URL 必须以 rtsp:// 开头`,
  vStreamSize: (label: string) => `${label}:宽/高未填 —— 点“探测”自动回填,或手动填写`,
  vStreamFps: (label: string) => `${label}:帧率未填`,
  vStreamBitrate: (label: string) => `${label}:码率未填`,

  // DeviceModal — per-stream probe
  fillRtspFirst: "请先填写 RTSP URL。",
  probingConn: "探测连通性…",
  rtspOkShort: (auth: string, latencyMs: number, codecs: string) =>
    `连通 · 认证 ${auth} · 延迟 ${latencyMs}ms · 编码: ${codecs}`,
  filledBack: (codec: string, width: number, height: number, fps: number) =>
    `已回填: ${codec} ${width}×${height} @${fps}fps`,
  bitrateMeasured: (kbps: number) => ` · 实测码率 ${kbps} kbps`,
  bitrateUnknown: " · 码率未知,保留手填值",
  ffmpegManual: "ffmpeg 不可用,分辨率/帧率请手填。",
  fillFailed: (detail: string) => `回填失败: ${detail}`,

  // ConfigEditor
  configNote: "config.yaml 全文。开启 Web 认证后才建议在此保存明文摄像头密码。",
  btnValidate: "校验",
  busyValidate: "校验中…",
  btnSaveApply: "保存并生效",
  busySave: "保存中…",
  btnReload: "重新加载",
  processing: "处理中…",
  validatePassed: "校验通过,未落盘。",
  savedReloaded: "已保存并热重载。",
  configRejected: (detail: string) => `配置被拒绝: ${detail}`,
  loadConfigFailed: (detail: string) => `加载配置失败: ${detail}`,

  // api.ts (non-component transport errors)
  requestTimeout: "请求超时,请重试。",
  networkError: (msg: string) => `网络错误: ${msg}`,
};

// Dict is the canonical string set; `en` must implement it exactly.
export type Dict = typeof zh;

const en: Dict = {
  appTitle: "ONVIF Proxy",
  docTitle: "ONVIF Proxy Admin",

  statVersion: "Version",
  statFfmpeg: "ffmpeg",
  statFfmpegOk: "available",
  statFfmpegNo: "unavailable",

  configHeading: "Configuration",
  devicesHeading: "Devices",

  addDevice: "➕ Add Device",
  loadDevicesFailed: (detail: string) => `Failed to load devices: ${detail}`,
  loading: "Loading…",
  noDevices: "No devices yet.",

  deviceRunning: "Running",
  deviceStopped: "Stopped",
  btnTestConn: "Test",
  btnSnapshot: "Snapshot",
  btnPreview: "Preview",
  btnOnvifCheck: "ONVIF Check",
  btnEdit: "Edit",
  btnDelete: "Delete",
  busyProbing: "Probing…",
  busySnapshot: "Capturing…",
  busyOnvif: "Checking…",
  busyDelete: "Deleting…",
  capturingSnapshot: "Capturing snapshot…",
  confirmDelete: (name: string) => `Delete device “${name}”? This hot-reloads the config immediately.`,

  rtspResultLine: (status: number, auth: string, latencyMs: number) =>
    `Connected · status ${status} · auth ${auth} · latency ${latencyMs}ms`,
  rtspCodecLine: (codecs: string, server: string) => `Codecs: ${codecs} · Server: ${server}`,
  rtspProbeFailed: "Probe failed.",
  hintDialTimeout: "TCP unreachable — check the IP and port.",
  hintAuthFailed: "Authentication failed — check the username and password.",
  hintNotFound: "Path not found (404) — check the RTSP path.",
  hintNoVideoTrack: "No video track — the SDP contains no video.",
  hintProtocolError: "RTSP protocol handshake failed.",

  thMethod: "Method",
  thResult: "Result",
  altSnapshot: "Snapshot",

  requestFailed: (detail: string) => `Request failed: ${detail}`,
  snapshotFailed: (detail: string) => `Snapshot failed: ${detail}`,
  deleteFailed: (detail: string) => `Delete failed: ${detail}`,

  previewClose: "Close",
  previewAlt: "Live preview",

  titleAdd: "Add Device",
  titleEdit: "Edit Device",
  fieldDeviceName: "Device name",
  phDeviceName: "Garage camera",
  fieldStreamName: "Name",
  fieldRtspUrl: "RTSP URL",
  phRtspUrl: "rtsp://user:pass@host:554/path",
  btnProbe: "Probe",
  btnRemove: "Remove",
  fieldWidth: "Width",
  fieldHeight: "Height",
  fieldFramerate: "Frame rate",
  fieldBitrate: "Bitrate (kbps)",
  addSubStream: "+ Add sub-stream",
  fieldSoapPort: "SOAP port",
  fieldRtspPort: "RTSP port",
  fieldOnvifUser: "ONVIF username (optional)",
  fieldOnvifPass: "ONVIF password (optional)",
  phKeepPass: "Leave blank = keep current password",
  rtspAuthNote:
    "The credentials in the RTSP URL (user:pass@) are used only by this proxy to grab snapshots and probe streams — they are never handed to ONVIF clients. Clients authenticate directly against the real camera when pulling video, so the credentials you enter when adopting the device in Unifi Protect and similar clients must be the camera's own RTSP credentials.",
  onvifAuthNote:
    "ONVIF authentication (WSSE) is a separate layer from the RTSP password above: it protects the virtual device's ONVIF interface. Blank = no check (recommended). If you do set it, use the same credentials as the camera's RTSP login — Unifi Protect only accepts one credential pair and applies it to both the ONVIF and RTSP layers.",

  btnCancel: "Cancel",
  btnSave: "Save",
  btnAdd: "Add",
  busySubmit: "Submitting…",
  saveFailed: "Save failed",
  addFailed: "Add failed",

  unnamedStream: "unnamed",
  streamRowLabel: (index: number, name: string) => `Stream ${index} (${name})`,
  vNameEmpty: "Device name is required",
  vSoapPort: "SOAP port is invalid",
  vRtspPort: "RTSP port is invalid",
  vStreamName: (label: string) => `${label}: name must be lowercase letters, digits, or underscores`,
  vStreamRtsp: (label: string) => `${label}: RTSP URL must start with rtsp://`,
  vStreamSize: (label: string) => `${label}: width/height missing — click Probe to autofill, or enter manually`,
  vStreamFps: (label: string) => `${label}: frame rate is required`,
  vStreamBitrate: (label: string) => `${label}: bitrate is required`,

  fillRtspFirst: "Enter the RTSP URL first.",
  probingConn: "Testing connectivity…",
  rtspOkShort: (auth: string, latencyMs: number, codecs: string) =>
    `Connected · auth ${auth} · latency ${latencyMs}ms · codecs: ${codecs}`,
  filledBack: (codec: string, width: number, height: number, fps: number) =>
    `Autofilled: ${codec} ${width}×${height} @${fps}fps`,
  bitrateMeasured: (kbps: number) => ` · measured bitrate ${kbps} kbps`,
  bitrateUnknown: " · bitrate unknown, keeping your value",
  ffmpegManual: "ffmpeg unavailable — enter resolution/frame rate manually.",
  fillFailed: (detail: string) => `Autofill failed: ${detail}`,

  configNote: "Full config.yaml. Store plaintext camera passwords here only after enabling web auth.",
  btnValidate: "Validate",
  busyValidate: "Validating…",
  btnSaveApply: "Save & Apply",
  busySave: "Saving…",
  btnReload: "Reload",
  processing: "Working…",
  validatePassed: "Validation passed — not saved.",
  savedReloaded: "Saved and hot-reloaded.",
  configRejected: (detail: string) => `Config rejected: ${detail}`,
  loadConfigFailed: (detail: string) => `Failed to load config: ${detail}`,

  requestTimeout: "Request timed out. Please retry.",
  networkError: (msg: string) => `Network error: ${msg}`,
};

export const dict: Record<LangKey, Dict> = { zh, en };

// detectLang resolves the initial language: an explicit localStorage choice
// wins; otherwise a navigator.language starting with "zh" picks Chinese.
function detectLang(): LangKey {
  try {
    const saved = localStorage.getItem("lang");
    if (saved === "zh" || saved === "en") return saved;
  } catch {
    /* localStorage unavailable (private mode) — fall through */
  }
  const nav = typeof navigator !== "undefined" ? navigator.language : "";
  return nav && nav.toLowerCase().startsWith("zh") ? "zh" : "en";
}

// currentLang mirrors the active language for non-hook consumers (api.ts). It is
// kept in sync by setLang, which App calls on every switch.
let currentLang: LangKey = detectLang();

export function getLang(): LangKey {
  return currentLang;
}

export function setLang(lang: LangKey): void {
  currentLang = lang;
  try {
    localStorage.setItem("lang", lang);
  } catch {
    /* ignore persistence failure */
  }
}

// tr reads a key from the active dictionary without a hook, for modules that run
// outside the component tree (e.g. api.ts transport errors).
export function tr<K extends keyof Dict>(key: K): Dict[K] {
  return dict[currentLang][key];
}

// LangContext carries the active language plus a setter down the tree. App owns
// the state and provides it; useT/useLang read it so a switch re-renders
// everything.
export const LangContext = createContext<{ lang: LangKey; setLang: (lang: LangKey) => void }>({
  lang: currentLang,
  setLang: () => {},
});

export function useLang(): { lang: LangKey; setLang: (lang: LangKey) => void } {
  return useContext(LangContext);
}

export function useT(): Dict {
  return dict[useContext(LangContext).lang];
}
