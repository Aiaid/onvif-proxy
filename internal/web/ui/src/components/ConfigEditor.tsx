import type { ComponentChild } from "preact";
import { useEffect, useState } from "preact/hooks";
import { apiText, errText } from "../api";
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
  const [text, setText] = useState("");
  const [msg, setMsg] = useState<ComponentChild>(null);

  const load = async () => {
    try {
      const t = await apiText("/api/config");
      setText(t);
      setMsg(null);
    } catch (e) {
      setMsg(<Msg kind="bad">加载配置失败: {errText(e)}</Msg>);
    }
  };

  useEffect(() => {
    void load();
  }, [refreshToken]);

  const put = (dryRun: boolean) => async () => {
    setMsg(<span class="muted">处理中…</span>);
    try {
      await apiText("/api/config" + (dryRun ? "?dry_run=1" : ""), {
        method: "PUT",
        headers: { "Content-Type": "text/plain" },
        body: text,
      });
      setMsg(<Msg kind="ok">{dryRun ? "校验通过,未落盘。" : "已保存并热重载。"}</Msg>);
      if (!dryRun) onApplied();
    } catch (e) {
      const detail =
        e && typeof e === "object" && "detail" in e ? (e as { detail: string }).detail : "";
      setMsg(
        <>
          <Msg kind="bad">配置被拒绝: {errText(e)}</Msg>
          {detail && <pre>{detail}</pre>}
        </>,
      );
    }
  };

  return (
    <div class="card">
      <p class="muted">config.yaml 全文。开启 Web 认证后才建议在此保存明文摄像头密码。</p>
      <textarea spellcheck={false} value={text} onInput={(e) => setText(e.currentTarget.value)} />
      <div class="btns">
        <AsyncButton onClick={put(true)} busyText="校验中…">
          校验
        </AsyncButton>
        <AsyncButton className="primary" onClick={put(false)} busyText="保存中…">
          保存并生效
        </AsyncButton>
        <button type="button" onClick={() => void load()}>
          重新加载
        </button>
      </div>
      {msg}
    </div>
  );
}
