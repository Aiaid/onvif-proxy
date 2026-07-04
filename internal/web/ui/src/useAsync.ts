import { useCallback, useRef, useState } from "preact/hooks";

// useAsync wraps an async action with a `busy` flag and re-entrancy guard. The
// returned `run` sets busy=true synchronously before awaiting, ignores calls
// made while already busy (double-click / double-submit protection), and always
// clears busy in a finally. This is the single mechanism every async button and
// form submit in the UI goes through.
export function useAsync(): {
  busy: boolean;
  run: (fn: () => Promise<void>) => Promise<void>;
} {
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);

  const run = useCallback(async (fn: () => Promise<void>) => {
    if (busyRef.current) return; // already in flight
    busyRef.current = true;
    setBusy(true);
    try {
      await fn();
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  }, []);

  return { busy, run };
}
