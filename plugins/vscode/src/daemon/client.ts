import type { MaintenanceInfo } from "./types";

export const TOKEN_HEADER = "x-nipa-daemon-token";

export class DaemonError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: string,
    readonly maintenance?: MaintenanceInfo | null,
  ) {
    super(message);
    this.name = "DaemonError";
  }
}

interface ErrorBody {
  code?: string;
  message?: string;
  maintenance?: MaintenanceInfo | null;
}

export interface CallOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
}

export class DaemonClient {
  unaryTimeoutMs = 30_000;

  constructor(
    readonly baseUrl: string,
    private readonly token: string,
    private readonly fetchImpl: typeof fetch = fetch,
  ) {}

  async call<T>(method: string, body: object = {}, opts: CallOptions = {}): Promise<T> {
    const { signal, dispose } = linkSignal(opts.signal, opts.timeoutMs ?? this.unaryTimeoutMs);
    try {
      const res = await this.post(method, body, signal);
      return (await res.json()) as T;
    } finally {
      dispose();
    }
  }

  async stream<T>(method: string, body: object, onMessage: (msg: T) => void, signal?: AbortSignal): Promise<void> {
    const res = await this.post(method, body, signal);
    if (!res.body) {
      return;
    }
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffered = "";
    const emit = (line: string) => {
      if (line.trim() === "") {
        return;
      }
      const parsed = JSON.parse(line) as T & { error?: unknown };
      if (parsed.error && typeof parsed.error === "object") {
        throw toError(500, parsed.error as ErrorBody);
      }
      onMessage(parsed);
    };
    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }
      buffered += decoder.decode(value, { stream: true });
      let nl = buffered.indexOf("\n");
      while (nl >= 0) {
        emit(buffered.slice(0, nl));
        buffered = buffered.slice(nl + 1);
        nl = buffered.indexOf("\n");
      }
    }
    emit(buffered + decoder.decode());
  }

  private async post(method: string, body: object, signal?: AbortSignal): Promise<Response> {
    const res = await this.fetchImpl(`${this.baseUrl}/v1/${method}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", [TOKEN_HEADER]: this.token },
      body: JSON.stringify(body),
      signal,
    });
    if (!res.ok) {
      let parsed: ErrorBody = {};
      try {
        parsed = (await res.json()) as ErrorBody;
      } catch {
        parsed = { message: res.statusText };
      }
      throw toError(res.status, parsed);
    }
    return res;
  }
}

function toError(status: number, body: ErrorBody): DaemonError {
  return new DaemonError(body.message || `daemon error ${status}`, status, body.code ?? "Unknown", body.maintenance);
}

function linkSignal(parent: AbortSignal | undefined, timeoutMs: number): { signal: AbortSignal; dispose: () => void } {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(new Error(`daemon call timed out after ${timeoutMs} ms`)), timeoutMs);
  const onAbort = () => controller.abort(parent?.reason);
  if (parent?.aborted) {
    controller.abort(parent.reason);
  }
  parent?.addEventListener("abort", onAbort);
  return {
    signal: controller.signal,
    dispose: () => {
      clearTimeout(timer);
      parent?.removeEventListener("abort", onAbort);
    },
  };
}
