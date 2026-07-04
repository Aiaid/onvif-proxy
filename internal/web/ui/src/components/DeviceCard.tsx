import type { ComponentChild } from "preact";
import { useState } from "preact/hooks";
import { apiBlob, apiJSON, errText } from "../api";
import { useT } from "../i18n";
import type { Dict } from "../i18n";
import type { DeviceView, OnvifCheck, RTSPResult } from "../types";
import { AsyncButton } from "./AsyncButton";
import { Msg } from "./Msg";
import { Preview } from "./Preview";

// rtspHint maps a backend err_kind to its localised explanation.
function rtspHint(t: Dict, kind: string): string {
  switch (kind) {
    case "dial_timeout":
      return t.hintDialTimeout;
    case "auth_failed":
      return t.hintAuthFailed;
    case "not_found":
      return t.hintNotFound;
    case "no_video_track":
      return t.hintNoVideoTrack;
    case "protocol_error":
      return t.hintProtocolError;
    default:
      return t.rtspProbeFailed;
  }
}

function rtspResultView(t: Dict, j: RTSPResult): ComponentChild {
  if (j.ok) {
    const tracks = (j.tracks || []).map((x) => `${x.type}:${x.codec}`).join(", ");
    return (
      <Msg kind="ok">
        {t.rtspResultLine(j.status, j.auth, j.latency_ms)}
        <br />
        {t.rtspCodecLine(tracks || "-", j.server || "-")}
      </Msg>
    );
  }
  return (
    <Msg kind="bad">
      {rtspHint(t, j.err_kind)}
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
  const t = useT();
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
    setOut(<span class="muted">{t.busyProbing}</span>);
    try {
      const j = await apiJSON<RTSPResult>("/api/test/rtsp", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url: primary?.rtsp_uri || "" }),
      });
      setOut(rtspResultView(t, j));
    } catch (e) {
      setOut(<Msg kind="bad">{t.requestFailed(errText(e))}</Msg>);
    }
  };

  const snapshot = async () => {
    setOut(<span class="muted">{t.capturingSnapshot}</span>);
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
      setOut(<Msg kind="bad">{t.snapshotFailed(errText(e))}</Msg>);
    }
  };

  const onvif = async () => {
    setOut(<span class="muted">{t.busyOnvif}</span>);
    try {
      const rows = await apiJSON<OnvifCheck[]>(
        "/api/test/onvif?device=" + encodeURIComponent(device.uuid),
        { method: "POST" },
      );
      setOut(
        <table>
          <tr>
            <th>{t.thMethod}</th>
            <th>HTTP</th>
            <th>Fault</th>
            <th>{t.thResult}</th>
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
      setOut(<Msg kind="bad">{t.requestFailed(errText(e))}</Msg>);
    }
  };

  const remove = async () => {
    try {
      await apiJSON<{ status: string }>("/api/devices/" + encodeURIComponent(device.uuid), {
        method: "DELETE",
      });
      onChanged();
    } catch (e) {
      setOut(<Msg kind="bad">{t.deleteFailed(errText(e))}</Msg>);
    }
  };

  return (
    <div class="card">
      <div class="name">
        {device.name} <span class={`dot ${device.running ? "on" : "off"}`} />
        <span class="muted" style="font-size:12px">
          {device.running ? t.deviceRunning : t.deviceStopped}
        </span>
      </div>
      <div class="ep">ONVIF: {device.endpoints.device_service}</div>
      <div class="ep">RTSP: {primary?.rtsp_uri || ""}</div>
      <div class="btns">
        <AsyncButton onClick={testRTSP} busyText={t.busyProbing}>
          {t.btnTestConn}
        </AsyncButton>
        <AsyncButton onClick={snapshot} busyText={t.busySnapshot}>
          {t.btnSnapshot}
        </AsyncButton>
        <button type="button" onClick={() => setShowPreview(true)}>
          {t.btnPreview}
        </button>
        <AsyncButton onClick={onvif} busyText={t.busyOnvif}>
          {t.btnOnvifCheck}
        </AsyncButton>
        <button type="button" onClick={() => onEdit(device)}>
          {t.btnEdit}
        </button>
        <AsyncButton
          className="danger"
          busyText={t.busyDelete}
          confirm={t.confirmDelete(device.name || device.uuid)}
          onClick={remove}
        >
          {t.btnDelete}
        </AsyncButton>
      </div>
      <div class="out">
        {out}
        {snapURL && <img src={snapURL} alt={t.altSnapshot} />}
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
