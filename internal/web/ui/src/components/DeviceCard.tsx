import type { ComponentChild } from "preact";
import { useState } from "preact/hooks";
import { apiBlob, apiJSON, errText } from "../api";
import type { DeviceView, OnvifCheck, RTSPResult } from "../types";
import { AsyncButton } from "./AsyncButton";
import { Msg } from "./Msg";
import { Preview } from "./Preview";

const RTSP_HINT: Record<string, string> = {
  dial_timeout: "TCP 连不通,检查 IP 与端口。",
  auth_failed: "认证失败,检查用户名与密码。",
  not_found: "路径 404,检查 RTSP 路径。",
  no_video_track: "未发现视频轨,SDP 中无 video。",
  protocol_error: "RTSP 协议握手异常。",
};

function rtspResultView(j: RTSPResult): ComponentChild {
  if (j.ok) {
    const tracks = (j.tracks || []).map((t) => `${t.type}:${t.codec}`).join(", ");
    return (
      <Msg kind="ok">
        连通 · 状态 {j.status} · 认证 {j.auth} · 延迟 {j.latency_ms}ms
        <br />
        编码: {tracks || "-"} · Server: {j.server || "-"}
      </Msg>
    );
  }
  const hint = RTSP_HINT[j.err_kind] || "探测失败。";
  return (
    <Msg kind="bad">
      {hint}
      <br />
      <span class="muted">
        {j.err_kind || ""}: {j.err_detail || ""}
      </span>
    </Msg>
  );
}

interface Props {
  device: DeviceView;
  onEdit: (device: DeviceView) => void;
  onChanged: () => void; // called after a successful delete to refresh lists
}

// DeviceCard renders one device plus its action row. Every action button routes
// through AsyncButton, so each one disables and shows a spinner while its
// request is in flight; the shared `out` region below the buttons holds the
// latest result. Preview is a synchronous overlay toggle (no request to guard).
export function DeviceCard({ device, onEdit, onChanged }: Props) {
  const [out, setOut] = useState<ComponentChild>(null);
  const [showPreview, setShowPreview] = useState(false);
  const [snapURL, setSnapURL] = useState<string | null>(null);

  const primary = device.endpoints.streams[0];

  const setSnapshot = (url: string | null) => {
    // Revoke the previous object URL before replacing it to avoid leaks.
    setSnapURL((prev) => {
      if (prev) URL.revokeObjectURL(prev);
      return url;
    });
  };

  const testRTSP = async () => {
    setOut(<span class="muted">探测中…</span>);
    try {
      const j = await apiJSON<RTSPResult>("/api/test/rtsp", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url: primary?.rtsp_uri || "" }),
      });
      setOut(rtspResultView(j));
    } catch (e) {
      setOut(<Msg kind="bad">请求失败: {errText(e)}</Msg>);
    }
  };

  const snapshot = async () => {
    setOut(<span class="muted">抓取快照…</span>);
    setSnapshot(null);
    try {
      const url =
        "/api/test/snapshot?device=" +
        encodeURIComponent(device.uuid) +
        "&stream=" +
        encodeURIComponent(primary?.name || "");
      const blob = await apiBlob(url);
      const obj = URL.createObjectURL(blob);
      setSnapshot(obj);
      setOut(null);
    } catch (e) {
      setOut(<Msg kind="bad">快照失败: {errText(e)}</Msg>);
    }
  };

  const onvif = async () => {
    setOut(<span class="muted">自检中…</span>);
    try {
      const rows = await apiJSON<OnvifCheck[]>(
        "/api/test/onvif?device=" + encodeURIComponent(device.uuid),
        { method: "POST" },
      );
      setOut(
        <table>
          <tr>
            <th>方法</th>
            <th>HTTP</th>
            <th>Fault</th>
            <th>结果</th>
          </tr>
          {rows.map((c) => (
            <tr key={c.method}>
              <td>{c.method}</td>
              <td>{c.http_status}</td>
              <td>{c.soap_fault || "-"}</td>
              <td>{c.pass ? "✅" : "❌"}</td>
            </tr>
          ))}
        </table>,
      );
    } catch (e) {
      setOut(<Msg kind="bad">请求失败: {errText(e)}</Msg>);
    }
  };

  const remove = async () => {
    try {
      await apiJSON<{ status: string }>("/api/devices/" + encodeURIComponent(device.uuid), {
        method: "DELETE",
      });
      onChanged();
    } catch (e) {
      setOut(<Msg kind="bad">删除失败: {errText(e)}</Msg>);
    }
  };

  return (
    <div class="card">
      <div class="name">
        {device.name} <span class={`dot ${device.running ? "on" : "off"}`} />
        <span class="muted" style="font-size:12px">
          {device.running ? "运行中" : "未运行"}
        </span>
      </div>
      <div class="ep">ONVIF: {device.endpoints.device_service}</div>
      <div class="ep">RTSP: {primary?.rtsp_uri || ""}</div>
      <div class="btns">
        <AsyncButton onClick={testRTSP} busyText="探测中…">
          测试连接
        </AsyncButton>
        <AsyncButton onClick={snapshot} busyText="抓取中…">
          快照
        </AsyncButton>
        <button type="button" onClick={() => setShowPreview(true)}>
          预览
        </button>
        <AsyncButton onClick={onvif} busyText="自检中…">
          ONVIF 自检
        </AsyncButton>
        <button type="button" onClick={() => onEdit(device)}>
          编辑
        </button>
        <AsyncButton
          className="danger"
          busyText="删除中…"
          confirm={`确定删除设备“${device.name || device.uuid}”?此操作会立即热重载配置。`}
          onClick={remove}
        >
          删除
        </AsyncButton>
      </div>
      <div class="out">
        {out}
        {snapURL && <img src={snapURL} alt="快照" />}
      </div>
      {showPreview && primary && (
        <Preview
          uuid={device.uuid}
          stream={primary.name}
          onClose={() => setShowPreview(false)}
        />
      )}
    </div>
  );
}
