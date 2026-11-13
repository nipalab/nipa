import type { Commit, FileHit } from "@/api/desktop";
import { compareMatches, fuzzyMatch, type Match } from "@/lib/fuzzy";

export type RootedHit = { root: string; hit: FileHit; match: Match };

// mergeFileHits ranks the hits of every clone together so the best file wins
// whichever clone it lives in; ties keep the clone order given.
export function mergeFileHits(results: { root: string; hits: FileHit[] }[], query: string, limit: number): RootedHit[] {
  const out: (RootedHit & { order: number })[] = [];
  results.forEach(({ root, hits }, order) => {
    for (const hit of hits) {
      const match = fuzzyMatch(query, hit.path);
      if (match) out.push({ root, hit, match, order });
    }
  });
  out.sort((a, b) => compareMatches(a.match, b.match, a.hit.path, b.hit.path) || a.order - b.order);
  return out.slice(0, limit).map(({ root, hit, match }) => ({ root, hit, match }));
}

// mrNumber reads "12", "#12" or "!12" as a merge request number.
export function mrNumber(query: string): number | null {
  const m = /^[#!]?(\d{1,9})$/.exec(query.trim());
  return m ? Number(m[1]) : null;
}

// looksLikeCommitId accepts what a base36 commit id can be.
export const looksLikeCommitId = (query: string): boolean => /^[0-9a-z]{4,20}$/.test(query.trim());

// commitMatches keeps loaded commits whose id or hash starts with the query
// or whose message contains every word of it, id and hash matches first.
export function commitMatches(commits: Commit[], query: string, limit: number): Commit[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  const words = q.split(/\s+/);
  const exact: Commit[] = [];
  const byMessage: Commit[] = [];
  for (const c of commits) {
    if (c.id.toLowerCase().startsWith(q) || (q.length >= 4 && c.hash.toLowerCase().startsWith(q))) exact.push(c);
    else if (words.every((w) => c.message.toLowerCase().includes(w))) byMessage.push(c);
  }
  return [...exact, ...byMessage].slice(0, limit);
}
