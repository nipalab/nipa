// A sparse selection is a set of folder prefixes; a folder is covered when it
// or one of its ancestors is selected.

const within = (path: string, prefix: string) => path === prefix || path.startsWith(`${prefix}/`);

export function normalizeSelection(paths: string[]): string[] {
  const clean = [...new Set(paths.map((p) => p.replace(/^\/+|\/+$/g, "")).filter(Boolean))].sort();
  return clean.filter((p) => !clean.some((q) => q !== p && within(p, q)));
}

export const isCovered = (path: string, selection: string[]) => selection.some((p) => within(path, p));

export const coveredByAncestor = (path: string, selection: string[]) => selection.some((p) => p !== path && within(path, p));

export const hasSelectedDescendant = (path: string, selection: string[]) => selection.some((p) => p !== path && within(p, path));

export function toggleFolder(selection: string[], path: string): string[] {
  if (coveredByAncestor(path, selection)) return selection;
  if (selection.includes(path)) return selection.filter((p) => p !== path);
  return normalizeSelection([...selection.filter((p) => !within(p, path)), path]);
}

export type RepoAddress = { server: string; org: string; project: string };

// parseRepoUrl splits a clone URL (<server>/<org>/<project>[/path]).
export function parseRepoUrl(url: string): RepoAddress | null {
  const m = /^(https?:\/\/[^/]+)\/([^/]+)\/([^/]+)/.exec(url.trim());
  return m ? { server: m[1], org: m[2], project: m[3] } : null;
}

export function joinPath(parent: string, name: string): string {
  const sep = parent.includes("\\") && !parent.includes("/") ? "\\" : "/";
  return parent.replace(/[\\/]+$/, "") + sep + name;
}
