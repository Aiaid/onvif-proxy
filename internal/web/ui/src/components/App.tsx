import { useCallback, useEffect, useState } from "preact/hooks";
import { dict, getLang, LangContext, setLang, useT, type LangKey } from "../i18n";
import type { DeviceView } from "../types";
import { ConfigEditor } from "./ConfigEditor";
import { DeviceList } from "./DeviceList";
import { DeviceModal } from "./DeviceModal";
import { StatusBar } from "./StatusBar";

interface ModalState {
  mode: "add" | "edit";
  device?: DeviceView;
}

// Inner holds the actual layout so it can read the active dictionary via useT
// (which needs to be inside LangContext.Provider).
function Inner() {
  const t = useT();
  const [refreshToken, setRefreshToken] = useState(0);
  const [modal, setModal] = useState<ModalState | null>(null);

  const refresh = useCallback(() => setRefreshToken((n) => n + 1), []);

  return (
    <>
      <StatusBar />
      <main>
        <DeviceList
          refreshToken={refreshToken}
          onAdd={() => setModal({ mode: "add" })}
          onEdit={(device) => setModal({ mode: "edit", device })}
          onChanged={refresh}
        />

        <h2>{t.configHeading}</h2>
        <ConfigEditor refreshToken={refreshToken} onApplied={refresh} />
      </main>

      {modal && (
        <DeviceModal
          mode={modal.mode}
          {...(modal.device ? { device: modal.device } : {})}
          onClose={() => setModal(null)}
          onSaved={refresh}
        />
      )}
    </>
  );
}

// App owns the language state at the top and provides it via LangContext, so a
// switch re-renders the whole tree. A single refreshToken drives cross-component
// reloads: bumping it re-fetches the device list and the config editor after any
// mutation (add / edit / delete / save).
export function App() {
  const [lang, setLangState] = useState<LangKey>(getLang);

  const changeLang = useCallback((next: LangKey) => {
    setLang(next); // persist + sync the non-hook module state (api.ts)
    setLangState(next);
  }, []);

  // Keep <html lang> and the document title in sync with the active language.
  useEffect(() => {
    document.documentElement.lang = lang === "zh" ? "zh-CN" : "en";
    document.title = dict[lang].docTitle;
  }, [lang]);

  return (
    <LangContext.Provider value={{ lang, setLang: changeLang }}>
      <Inner />
    </LangContext.Provider>
  );
}
