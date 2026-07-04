import { useEffect, useState } from "preact/hooks";
import { apiJSON } from "../api";
import { useLang, useT } from "../i18n";
import type { StatusView } from "../types";

// StatusBar polls /api/status and shows version / ffmpeg / advertise_ip. Poll
// failures are swallowed (transient) and leave the last good values on screen.
// It also hosts the language switch on the right.
export function StatusBar() {
  const t = useT();
  const { lang, setLang } = useLang();
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
      <h1>{t.appTitle}</h1>
      <span class="stat">
        {t.statVersion} <b>{status?.version || "…"}</b>
      </span>
      <span class="stat">
        {t.statFfmpeg} <b>{status ? (status.ffmpeg ? t.statFfmpegOk : t.statFfmpegNo) : "…"}</b>
      </span>
      <span class="stat">
        advertise_ip <b>{status?.advertise_ip || "…"}</b>
      </span>
      <span class="stat" style="margin-left:auto;display:flex;gap:6px">
        <button type="button" class={lang === "zh" ? "primary" : ""} onClick={() => setLang("zh")}>
          中文
        </button>
        <button type="button" class={lang === "en" ? "primary" : ""} onClick={() => setLang("en")}>
          EN
        </button>
      </span>
    </header>
  );
}
