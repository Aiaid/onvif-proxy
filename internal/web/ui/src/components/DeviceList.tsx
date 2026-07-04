import { useEffect, useState } from "preact/hooks";
import { apiJSON, errText } from "../api";
import { useT } from "../i18n";
import type { DeviceView } from "../types";
import { DeviceCard } from "./DeviceCard";
import { Msg } from "./Msg";

interface Props {
  refreshToken: number;
  onAdd: () => void;
  onEdit: (device: DeviceView) => void;
  onChanged: () => void;
}

// DeviceList loads /api/devices (on mount, whenever refreshToken changes, and on
// a 15s poll) and renders one DeviceCard per device.
export function DeviceList({ refreshToken, onAdd, onEdit, onChanged }: Props) {
  const t = useT();
  const [devices, setDevices] = useState<DeviceView[] | null>(null);
  const [error, setError] = useState<string>("");

  useEffect(() => {
    let alive = true;
    const load = async () => {
      try {
        const list = await apiJSON<DeviceView[]>("/api/devices");
        if (alive) {
          setDevices(list || []);
          setError("");
        }
      } catch (e) {
        if (alive) setError(errText(e));
      }
    };
    void load();
    const id = window.setInterval(load, 15000);
    return () => {
      alive = false;
      window.clearInterval(id);
    };
  }, [refreshToken]);

  return (
    <>
      <div class="row-head">
        <h2>{t.devicesHeading}</h2>
        <button type="button" class="primary" onClick={onAdd}>
          {t.addDevice}
        </button>
      </div>
      {error && <Msg kind="bad">{t.loadDevicesFailed(error)}</Msg>}
      {devices === null && !error && <p class="muted">{t.loading}</p>}
      {devices !== null && devices.length === 0 && <p class="muted">{t.noDevices}</p>}
      {devices?.map((d) => (
        <DeviceCard key={d.uuid} device={d} onEdit={onEdit} onChanged={onChanged} />
      ))}
    </>
  );
}
