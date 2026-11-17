import * as fs from "node:fs/promises";
import * as path from "node:path";

const MAX_PARENTS = 32;

export async function findRepoRoot(start: string, exists: (file: string) => Promise<boolean> = fileExists): Promise<string | undefined> {
  let dir = path.resolve(start);
  for (let i = 0; i <= MAX_PARENTS; i++) {
    if (await exists(path.join(dir, ".nipa", "config"))) {
      return dir;
    }
    const parent = path.dirname(dir);
    if (parent === dir) {
      return undefined;
    }
    dir = parent;
  }
  return undefined;
}

async function fileExists(file: string): Promise<boolean> {
  try {
    return (await fs.stat(file)).isFile();
  } catch {
    return false;
  }
}

export function relativeTo(root: string, file: string): string | undefined {
  const rel = path.relative(root, file);
  if (!rel || rel.startsWith("..") || path.isAbsolute(rel)) {
    return undefined;
  }
  return rel.split(path.sep).join("/");
}
