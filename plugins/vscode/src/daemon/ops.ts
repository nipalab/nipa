import { DaemonClient, DaemonError } from "./client";
import type { BlobEvent, BlobInfo, MaintenanceInfo, OpEvent, OpResult } from "./types";

export async function runOp(
  client: DaemonClient,
  method: string,
  body: object,
  onEvent?: (event: OpEvent) => void,
  signal?: AbortSignal,
): Promise<OpResult> {
  let result: OpResult | undefined;
  let failure: DaemonError | undefined;
  await client.stream<OpEvent>(
    method,
    body,
    (event) => {
      if (event.failure) {
        failure = new DaemonError(event.failure.message, event.failure.code, "OpFailure", event.failure.maintenance);
      } else if (event.result) {
        result = event.result;
      }
      onEvent?.(event);
    },
    signal,
  );
  if (failure) {
    throw failure;
  }
  if (!result) {
    throw new DaemonError(`${method} ended without a result`, 500, "Internal");
  }
  return result;
}

export interface Blob {
  info?: BlobInfo;
  data: Buffer;
}

export async function readBlob(
  client: DaemonClient,
  root: string,
  source: string,
  path: string,
  signal?: AbortSignal,
): Promise<Blob> {
  const parts: Buffer[] = [];
  let info: BlobInfo | undefined;
  let failure: DaemonError | undefined;
  await client.stream<BlobEvent>(
    "ReadBlob",
    { root, blob: { source, path } },
    (event) => {
      if (event.info) {
        info = event.info;
      } else if (event.data) {
        parts.push(Buffer.from(event.data, "base64"));
      } else if (event.failure) {
        failure = new DaemonError(event.failure.message, event.failure.code, "OpFailure", event.failure.maintenance);
      }
    },
    signal,
  );
  if (failure) {
    throw failure;
  }
  return { info, data: Buffer.concat(parts) };
}

export function describeError(err: unknown): string {
  if (err instanceof DaemonError) {
    return err.maintenance ? err.message + maintenanceSuffix(err.maintenance) : err.message;
  }
  if (err instanceof Error) {
    return err.message;
  }
  return String(err);
}

function maintenanceSuffix(info: MaintenanceInfo): string {
  return info.ends_at ? ` (estimated end ${new Date(info.ends_at).toLocaleString()})` : "";
}
