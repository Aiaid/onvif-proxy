import type { ComponentChild } from "preact";
import { useEffect, useState } from "preact/hooks";
import { apiText, errText } from "../api";
import { useT } from "../i18n";
import { AsyncButton } from "./AsyncButton";
import { Msg } from "./Msg";

interface Props {
  // Bumping this reloads the editor from the server (after an external change
  // such as add/edit/delete).
  refreshToken: number;
  // Called after a successful save so the rest of the UI can refresh.
  onApplied: () => void;
}

// ConfigEditor is the raw YAML editor with dry-run validate and save-and-apply.
// Both writes go through AsyncButton, so the buttons lock while the request is
// in flight. Save/validate post the textarea verbatim to PUT /api/config.
export function ConfigEditor({ refreshToken, onApplied }: Props) {
  const t = useT();
  const [text, setText] = useState("");
  const [msg, setMsg] = useState<ComponentChild>(null);

  const load = async () => {
    try {
      const cfg = await apiText("/api/config");
      setText(cfg);
      setMsg(null);
    } catch (e) {
      setMsg(<Msg kind="bad">{t.loadConfigFailed(errText(e))}</Msg>);
    }
  };

  useEffect(() => {
    void load();
  }, [refreshToken]);

  const put = (dryRun: boolean) => async () => {
    setMsg(<span class="muted">{t.processing}</span>);
    try {
      await apiText("/api/config" + (dryRun ? "?dry_run=1" : ""), {
        method: "PUT",
        headers: { "Content-Type": "text/plain" },
        body: text,
      });
      setMsg(<Msg kind="ok">{dryRun ? t.validatePassed : t.savedReloaded}</Msg>);
      if (!dryRun) onApplied();
    } catch (e) {
      const detail =
        e && typeof e === "object" && "detail" in e ? (e as { detail: string }).detail : "";
      setMsg(
        <>
          <Msg kind="bad">{t.configRejected(errText(e))}</Msg>
          {detail && <pre>{detail}</pre>}
        </>,
      );
    }
  };

  return (
    <div class="card">
      <p class="muted">{t.configNote}</p>
      <textarea spellcheck={false} value={text} onInput={(e) => setText(e.currentTarget.value)} />
      <div class="btns">
        <AsyncButton onClick={put(true)} busyText={t.busyValidate}>
          {t.btnValidate}
        </AsyncButton>
        <AsyncButton className="primary" onClick={put(false)} busyText={t.busySave}>
          {t.btnSaveApply}
        </AsyncButton>
        <button type="button" onClick={() => void load()}>
          {t.btnReload}
        </button>
      </div>
      {msg}
    </div>
  );
}
