import { useEffect, useState } from "preact/hooks";
import { apiJSON } from "../api";
import type { StatusView } from "../types";

// StatusBar polls /api/status and shows version / ffmpeg / advertise_ip. Poll
// failures are swallowed (transient) and leave the last good values on screen.
export function StatusBar() {
  const [status, setStatus] = useState<StatusView | null>(null);

  useEffect(() => {
    let alive = true;
    const load = async () => {
      try {
        const s = await apiJSON<StatusView>("/api/status");
        if (alive) setStatus(s);
      } catch {
        /* ignore transient poll errors */
      }
    };
    void load();
    const id = window.setInterval(load, 15000);
    return () => {
      alive = false;
      window.clearInterval(id);
    };
  }, []);

  return (
    <header>
      <h1>ONVIF Proxy</h1>
      <span class="stat">
        版本 <b>{status?.version || "…"}</b>
      </span>
      <span class="stat">
        ffmpeg <b>{status ? (status.ffmpeg ? "可用" : "不可用") : "…"}</b>
      </span>
      <span class="stat">
        advertise_ip <b>{status?.advertise_ip || "…"}</b>
      </span>
    </header>
  );
}
