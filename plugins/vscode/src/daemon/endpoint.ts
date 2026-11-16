import * as fs from "node:fs/promises";
import * as path from "node:path";

export interface Endpoint {
  pid: number;
  port: number;
  json_port: number;
  token: string;
  version: string;
}

export type Env = Record<string, string | undefined>;

export function defaultEndpointPath(env: Env, platform: NodeJS.Platform): string {
  const home = env.HOME || env.USERPROFILE || "";
  let dir: string;
  if (platform === "win32") {
    dir = env.APPDATA || path.join(home, "AppData", "Roaming");
  } else if (platform === "darwin") {
    dir = path.join(home, "Library", "Application Support");
  } else {
    dir = env.XDG_CONFIG_HOME || path.join(home, ".config");
  }
  return path.join(dir, "nipa", "daemon.json");
}

export async function readEndpoint(file: string): Promise<Endpoint | undefined> {
  let raw: string;
  try {
    raw = await fs.readFile(file, "utf8");
  } catch {
    return undefined;
  }
  try {
    const parsed = JSON.parse(raw) as Partial<Endpoint>;
    if (typeof parsed.pid !== "number" || typeof parsed.token !== "string") {
      return undefined;
    }
    return {
      pid: parsed.pid,
      port: parsed.port ?? 0,
      json_port: parsed.json_port ?? 0,
      token: parsed.token,
      version: parsed.version ?? "",
    };
  } catch {
    return undefined;
  }
}
