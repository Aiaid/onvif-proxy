import type { ComponentChildren } from "preact";
import { useT } from "../i18n";
import { useAsync } from "../useAsync";

interface Props {
  onClick: () => Promise<void>;
  children: ComponentChildren;
  // Text shown while the action is in flight (defaults to the localised
  // "processing…").
  busyText?: string;
  // Extra class names appended to the base button class.
  className?: string;
  // When set, window.confirm is shown first; a decline aborts without locking.
  confirm?: string;
  // Disable independently of the busy state (e.g. invalid form).
  disabled?: boolean;
  title?: string;
}

// AsyncButton is the only way async actions are triggered in this UI. On click
// it immediately disables itself and swaps its label for a spinner, runs the
// action through useAsync (which blocks re-entry), and restores on settle. An
// optional confirm() gate runs before the lock so a cancelled confirm leaves
// the button usable.
export function AsyncButton({ onClick, children, busyText, className = "", confirm, disabled = false, title }: Props) {
  const t = useT();
  const { busy, run } = useAsync();
  const label = busyText ?? t.processing;

  const handle = () => {
    if (busy || disabled) return;
    if (confirm && !window.confirm(confirm)) return;
    void run(onClick);
  };

  return (
    <button
      type="button"
      class={className}
      disabled={busy || disabled}
      aria-busy={busy}
      title={title}
      onClick={handle}
    >
      {busy ? (
        <span class="spin-wrap">
          <span class="spinner" aria-hidden="true" /> {label}
        </span>
      ) : (
        children
      )}
    </button>
  );
}
