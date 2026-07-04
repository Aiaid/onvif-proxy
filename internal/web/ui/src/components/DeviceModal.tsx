import type { ComponentChild } from "preact";
import { useEffect, useState } from "preact/hooks";
import { apiJSON, errText, jsonBody } from "../api";
import type { DeviceSpec, DeviceView, RTSPResult, StreamInfo } from "../types";
import { AsyncButton } from "./AsyncButton";
import { Msg } from "./Msg";

const SUB_NAMES = ["sub", "mobile", "third", "fourth"];

const RTSP_HINT: Record<string, string> = {
  dial_timeout: "TCP 连不通,检查 IP 与端口。",
  auth_failed: "认证失败,检查用户名与密码。",
  not_found: "路径 404,检查 RTSP 路径。",
  no_video_track: "未发现视频轨,SDP 中无 video。",
  protocol_error: "RTSP 协议握手异常。",
};

interface StreamRow {
  id: number;
  name: string;
  rtsp: string;
  width: string;
  height: string;
  framerate: string;
  bitrate: string;
  out: ComponentChild;
}

let rowSeq = 0;
function newRow(name: string, rtsp = "", init: Partial<StreamRow> = {}): StreamRow {
  return {
    id: rowSeq++,
    name,
    rtsp,
    width: "",
    height: "",
    framerate: "",
    bitrate: "4096",
    out: null,
    ...init,
  };
}

// suggestPorts derives the next free soap/rtsp port pair from the running
// devices (soap from 8081, rtsp from 8554), matching the old UI heuristic.
async function suggestPorts(): Promise<{ soap: number; rtsp: number }> {
  let soap = 8080;
  let rtsp = 8553;
  try {
    const list = await apiJSON<DeviceView[]>("/api/devices");
    for (const d of list || []) {
      if (d.soap_port > soap) soap = d.soap_port;
      if (d.rtsp_port > rtsp) rtsp = d.rtsp_port;
    }
  } catch {
    /* fall back to defaults */
  }
  return { soap: soap + 1, rtsp: rtsp + 1 };
}

interface Props {
  mode: "add" | "edit";
  device?: DeviceView; // present in edit mode
  onClose: () => void;
  onSaved: () => void;
}

// DeviceModal is the single form used for both adding and editing a device. In
// edit mode it prefills from the device DTO (including per-stream source
// parameters and the ONVIF username); the password field stays blank and, left
// untouched, the backend keeps the stored password. Submit and every per-stream
// probe route through AsyncButton, so the form cannot be double-submitted and
// each button disables while its request runs.
export function DeviceModal({ mode, device, onClose, onSaved }: Props) {
  const [name, setName] = useState(device?.name ?? "");
  const [soapPort, setSoapPort] = useState(device ? String(device.soap_port) : "");
  const [rtspPort, setRtspPort] = useState(device ? String(device.rtsp_port) : "");
  const [authUser, setAuthUser] = useState(device?.auth_user ?? "");
  const [authPass, setAuthPass] = useState("");
  const [msg, setMsg] = useState<ComponentChild>(null);

  const [streams, setStreams] = useState<StreamRow[]>(() => {
    if (device) {
      return device.endpoints.streams.map((s) =>
        newRow(s.name, s.rtsp, {
          width: s.width ? String(s.width) : "",
          height: s.height ? String(s.height) : "",
          framerate: s.framerate ? String(s.framerate) : "",
          bitrate: s.bitrate ? String(s.bitrate) : "4096",
        }),
      );
    }
    return [newRow("main")];
  });

  // In add mode, suggest the next free port pair once on open.
  useEffect(() => {
    if (mode !== "add") return;
    let alive = true;
    void suggestPorts().then((p) => {
      if (!alive) return;
      setSoapPort(String(p.soap));
      setRtspPort(String(p.rtsp));
    });
    return () => {
      alive = false;
    };
  }, [mode]);

  const patchRow = (id: number, patch: Partial<StreamRow>) =>
    setStreams((prev) => prev.map((s) => (s.id === id ? { ...s, ...patch } : s)));

  const addStream = () => {
    setStreams((prev) => {
      const idx = prev.length;
      const nm = idx === 0 ? "main" : SUB_NAMES[idx] || `stream${idx}`;
      return [...prev, newRow(nm)];
    });
  };

  const removeStream = (id: number) => setStreams((prev) => prev.filter((s) => s.id !== id));

  const probe = (row: StreamRow) => async () => {
    const url = row.rtsp.trim();
    if (!url) {
      patchRow(row.id, { out: <Msg kind="bad">请先填写 RTSP URL。</Msg> });
      return;
    }
    patchRow(row.id, { out: <span class="muted">探测连通性…</span> });
    try {
      const j = await apiJSON<RTSPResult>("/api/test/rtsp", jsonBody({ url }));
      if (!j.ok) {
        const hint = RTSP_HINT[j.err_kind] || "探测失败。";
        patchRow(row.id, {
          out: (
            <Msg kind="bad">
              {hint}
              <br />
              <span class="muted">
                {j.err_kind || ""}: {j.err_detail || ""}
              </span>
            </Msg>
          ),
        });
        return;
      }
      const tracks = (j.tracks || []).map((t) => `${t.type}:${t.codec}`).join(", ");
      patchRow(row.id, {
        out: (
          <Msg kind="ok">
            连通 · 认证 {j.auth} · 延迟 {j.latency_ms}ms · 编码: {tracks || "-"}
          </Msg>
        ),
      });
      await fillStreamInfo(row.id, url);
    } catch (e) {
      patchRow(row.id, { out: <Msg kind="bad">请求失败: {errText(e)}</Msg> });
    }
  };

  // fillStreamInfo probes resolution/framerate and back-fills the row inputs.
  const fillStreamInfo = async (id: number, url: string) => {
    try {
      const j = await apiJSON<StreamInfo>("/api/test/streaminfo", jsonBody({ url }));
      const patch: Partial<StreamRow> = {};
      if (j.width) patch.width = String(j.width);
      if (j.height) patch.height = String(j.height);
      if (j.fps) patch.framerate = String(j.fps);
      patch.out = (
        <Msg kind="ok">
          已回填: {j.codec || ""} {j.width}×{j.height} @{j.fps}fps
        </Msg>
      );
      patchRow(id, patch);
    } catch (e) {
      const info =
        e && typeof e === "object" && "status" in e && (e as { status: number }).status === 501
          ? "ffmpeg 不可用,分辨率/帧率请手填。"
          : "回填失败: " + errText(e);
      patchRow(id, { out: <Msg kind={info.startsWith("ffmpeg") ? "info" : "bad"}>{info}</Msg> });
    }
  };

  const submit = async () => {
    const num = (v: string) => parseInt(v, 10) || 0;
    const spec: DeviceSpec = {
      name: name.trim(),
      soap_port: num(soapPort),
      rtsp_port: num(rtspPort),
      streams: streams.map((s) => ({
        name: s.name.trim(),
        rtsp: s.rtsp.trim(),
        width: num(s.width),
        height: num(s.height),
        framerate: num(s.framerate),
        bitrate: num(s.bitrate),
      })),
    };
    const user = authUser.trim();
    const pass = authPass;
    if (user !== "" || pass !== "") {
      spec.auth = { username: user, password: pass };
    }

    setMsg(<span class="muted">提交中…</span>);
    try {
      if (mode === "edit" && device) {
        await apiJSON("/api/devices/" + encodeURIComponent(device.uuid), {
          method: "PUT",
          ...jsonBody(spec),
        });
      } else {
        await apiJSON("/api/devices", { method: "POST", ...jsonBody(spec) });
      }
      onSaved();
      onClose();
    } catch (e) {
      const detail =
        e && typeof e === "object" && "detail" in e ? (e as { detail: string }).detail : "";
      setMsg(
        <>
          <Msg kind="bad">{mode === "edit" ? "保存失败" : "添加失败"}: {errText(e)}</Msg>
          {detail && <pre>{detail}</pre>}
        </>,
      );
    }
  };

  return (
    <div class="modal" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div class="box">
        <h3>{mode === "edit" ? "编辑设备" : "新增设备"}</h3>
        <div class="field">
          <label>设备名称</label>
          <input value={name} placeholder="车库摄像头" onInput={(e) => setName(e.currentTarget.value)} />
        </div>

        <div>
          {streams.map((row, idx) => (
            <div class="stream-row" key={row.id}>
              <div class="top">
                <div class="field">
                  <label>名称</label>
                  <input value={row.name} onInput={(e) => patchRow(row.id, { name: e.currentTarget.value })} />
                </div>
                <div class="field" style="flex:3">
                  <label>RTSP URL</label>
                  <input
                    value={row.rtsp}
                    placeholder="rtsp://user:pass@host:554/path"
                    onInput={(e) => patchRow(row.id, { rtsp: e.currentTarget.value })}
                  />
                </div>
                <AsyncButton onClick={probe(row)} busyText="探测中…">
                  探测
                </AsyncButton>
                {idx !== 0 && (
                  <button type="button" class="danger" onClick={() => removeStream(row.id)}>
                    移除
                  </button>
                )}
              </div>
              <div class="grid4">
                <div class="field">
                  <label>宽</label>
                  <input type="number" value={row.width} onInput={(e) => patchRow(row.id, { width: e.currentTarget.value })} />
                </div>
                <div class="field">
                  <label>高</label>
                  <input type="number" value={row.height} onInput={(e) => patchRow(row.id, { height: e.currentTarget.value })} />
                </div>
                <div class="field">
                  <label>帧率</label>
                  <input type="number" value={row.framerate} onInput={(e) => patchRow(row.id, { framerate: e.currentTarget.value })} />
                </div>
                <div class="field">
                  <label>码率(kbps)</label>
                  <input type="number" value={row.bitrate} onInput={(e) => patchRow(row.id, { bitrate: e.currentTarget.value })} />
                </div>
              </div>
              {row.out && <div class="s-out">{row.out}</div>}
            </div>
          ))}
        </div>

        <p class="muted">
          RTSP URL 中的账密(user:pass@)只供本代理抓快照/探测使用,<b>不会</b>下发给 ONVIF 客户端;
          客户端拉流时由真实摄像头直接认证,所以在 Unifi Protect 等客户端里收编时填的账密必须是<b>摄像头本身的 RTSP 账密</b>。
        </p>
        <button type="button" onClick={addStream}>
          + 添加子码流
        </button>

        <div class="inline" style="margin-top:12px">
          <div class="field">
            <label>SOAP 端口</label>
            <input type="number" value={soapPort} onInput={(e) => setSoapPort(e.currentTarget.value)} />
          </div>
          <div class="field">
            <label>RTSP 端口</label>
            <input type="number" value={rtspPort} onInput={(e) => setRtspPort(e.currentTarget.value)} />
          </div>
        </div>

        <div class="inline">
          <div class="field">
            <label>ONVIF 用户名(可选)</label>
            <input value={authUser} onInput={(e) => setAuthUser(e.currentTarget.value)} />
          </div>
          <div class="field">
            <label>ONVIF 密码(可选)</label>
            <input
              type="password"
              value={authPass}
              placeholder={mode === "edit" && device?.auth_user ? "留空 = 保持原密码" : ""}
              onInput={(e) => setAuthPass(e.currentTarget.value)}
            />
          </div>
        </div>
        <p class="muted">
          ONVIF 认证(WSSE)与上面的 RTSP 密码是<b>独立的两层</b>:这里保护的是虚拟设备的 ONVIF 接口。
          留空 = 不校验(推荐)。若要设置,建议与摄像头 RTSP 账密<b>相同</b>——Unifi Protect 只让填一组账密,并同时用于 ONVIF 与 RTSP 两层。
        </p>

        {msg}
        <div class="actions">
          <button type="button" onClick={onClose}>
            取消
          </button>
          <AsyncButton className="primary" busyText="提交中…" onClick={submit}>
            {mode === "edit" ? "保存" : "添加"}
          </AsyncButton>
        </div>
      </div>
    </div>
  );
}
