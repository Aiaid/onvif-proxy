import { useCallback, useState } from "preact/hooks";
import type { DeviceView } from "../types";
import { ConfigEditor } from "./ConfigEditor";
import { DeviceList } from "./DeviceList";
import { DeviceModal } from "./DeviceModal";
import { StatusBar } from "./StatusBar";

interface ModalState {
  mode: "add" | "edit";
  device?: DeviceView;
}

// App wires the three sections together. A single refreshToken drives cross-
// component reloads: bumping it re-fetches the device list and the config
// editor after any mutation (add / edit / delete / save).
export function App() {
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

        <h2>配置</h2>
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
