import type { ComponentChildren } from "preact";

// Msg is the inline coloured status/result banner used across the UI.
export function Msg({ kind, children }: { kind: "ok" | "bad" | "info"; children: ComponentChildren }) {
  return <div class={`msg ${kind}`}>{children}</div>;
}

// Detail renders a monospace error detail block (validation output, stderr).
export function Detail({ text }: { text: string }) {
  if (!text) return null;
  return <pre>{text}</pre>;
}
