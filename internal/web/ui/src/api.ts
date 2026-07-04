// Uniform fetch wrapper for every request the UI makes. It centralises timeout
// handling, non-2xx -> thrown error conversion, and parsing of the server's
// {"error","detail"} envelope, so no component ever touches fetch directly.

const DEFAULT_TIMEOUT_MS = 20000;

// ApiError carries the HTTP status plus the server's error/detail envelope so
// callers can render a consistent message and detail block.
export class ApiError extends Error {
  status: number;
  detail: string;
  constructor(status: number, message: string, detail = "") {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.detail = detail;
  }
}

async function request(path: string, opts: RequestInit = {}, timeoutMs = DEFAULT_TIMEOUT_MS): Promise<Response> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(path, { ...opts, signal: controller.signal });
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") {
      throw new ApiError(0, "请求超时,请重试。");
    }
    const msg = e instanceof Error ? e.message : String(e);
    throw new ApiError(0, "网络错误: " + msg);
  } finally {
    clearTimeout(timer);
  }
}

// parseError reads a non-2xx response body as the standard error envelope,
// falling back to the raw text when it is not JSON.
async function parseError(r: Response): Promise<ApiError> {
  const text = await r.text().catch(() => "");
  if (text) {
    try {
      const j = JSON.parse(text) as { error?: string; detail?: string };
      return new ApiError(r.status, j.error || `HTTP ${r.status}`, j.detail || "");
    } catch {
      return new ApiError(r.status, `HTTP ${r.status}`, text);
    }
  }
  return new ApiError(r.status, `HTTP ${r.status}`);
}

// apiJSON issues a request and decodes a JSON body, throwing ApiError on any
// non-2xx status or transport failure.
export async function apiJSON<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const r = await request(path, opts);
  if (!r.ok) throw await parseError(r);
  const text = await r.text();
  return (text ? JSON.parse(text) : null) as T;
}

// apiText fetches a plain-text body (the YAML config editor).
export async function apiText(path: string, opts: RequestInit = {}): Promise<string> {
  const r = await request(path, opts);
  if (!r.ok) throw await parseError(r);
  return await r.text();
}

// apiBlob fetches a binary body (snapshot JPEG); error responses are JSON.
export async function apiBlob(path: string, opts: RequestInit = {}): Promise<Blob> {
  const r = await request(path, opts);
  if (!r.ok) throw await parseError(r);
  return await r.blob();
}

// jsonBody builds the headers+body pair for a JSON POST/PUT.
export function jsonBody(v: unknown): RequestInit {
  return { headers: { "Content-Type": "application/json" }, body: JSON.stringify(v) };
}

// errText renders any thrown value into a user-facing string.
export function errText(e: unknown): string {
  if (e instanceof ApiError) return e.detail ? `${e.message}(${e.detail})` : e.message;
  if (e instanceof Error) return e.message;
  return String(e);
}
