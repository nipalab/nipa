// Fuzzy matching shared by the command palette: the query's characters must
// appear in order, case-insensitively, spaces ignored. Results rank like the
// daemon's SearchFiles: basename prefix, basename substring, basename
// subsequence, then the same on the whole text; ties go to the tighter match,
// then the shorter text.

export type Match = { tier: number; span: number; positions: number[] };

const fold = (s: string) => Array.from(s, (c) => c.toLowerCase());

export function normalizeQuery(query: string): string[] {
  return fold(query).filter((c) => c.trim() !== "");
}

function substring(needle: string[], hay: string[], offset: number): number[] | null {
  outer: for (let start = 0; start + needle.length <= hay.length; start++) {
    for (let i = 0; i < needle.length; i++) if (hay[start + i] !== needle[i]) continue outer;
    return needle.map((_, i) => offset + start + i);
  }
  return null;
}

function subsequence(needle: string[], hay: string[], offset: number): { span: number; positions: number[] } | null {
  let best: { span: number; positions: number[] } | null = null;
  for (let start = 0; start < hay.length; start++) {
    if (hay[start] !== needle[0]) continue;
    const positions: number[] = [];
    let i = start;
    for (let j = 0; i < hay.length && j < needle.length; i++) {
      if (hay[i] === needle[j]) {
        positions.push(offset + i);
        j++;
      }
    }
    if (positions.length < needle.length) break;
    const span = i - start;
    if (!best || span < best.span) best = { span, positions };
  }
  return best;
}

export function fuzzyMatch(query: string, text: string): Match | null {
  const q = normalizeQuery(query);
  if (q.length === 0) return null;
  const all = fold(text);
  const slash = Math.max(text.lastIndexOf("/"), text.lastIndexOf("\\"));
  const baseOffset = slash < 0 ? 0 : Array.from(text.slice(0, slash + 1)).length;
  const base = all.slice(baseOffset);
  if (q.every((c, i) => base[i] === c)) return { tier: 0, span: q.length, positions: q.map((_, i) => baseOffset + i) };
  const inBase = substring(q, base, baseOffset);
  if (inBase) return { tier: 1, span: q.length, positions: inBase };
  const seqBase = subsequence(q, base, baseOffset);
  if (seqBase) return { tier: 2, ...seqBase };
  const inAll = substring(q, all, 0);
  if (inAll) return { tier: 3, span: q.length, positions: inAll };
  const seqAll = subsequence(q, all, 0);
  return seqAll ? { tier: 4, ...seqAll } : null;
}

export function compareMatches(a: Match, b: Match, aText: string, bText: string): number {
  return a.tier - b.tier || a.span - b.span || aText.length - bText.length || (aText < bText ? -1 : aText > bText ? 1 : 0);
}

export type Ranked<T> = { item: T; match: Match };

// rank keeps the items whose text matches query, best first, at most limit.
export function rank<T>(items: readonly T[], query: string, text: (item: T) => string, limit = Infinity): Ranked<T>[] {
  const out: (Ranked<T> & { text: string })[] = [];
  for (const item of items) {
    const t = text(item);
    const match = fuzzyMatch(query, t);
    if (match) out.push({ item, match, text: t });
  }
  out.sort((a, b) => compareMatches(a.match, b.match, a.text, b.text));
  return out.slice(0, limit).map(({ item, match }) => ({ item, match }));
}

export type Segment = { text: string; hit: boolean };

// segments splits text into runs for highlighting the matched characters.
export function segments(text: string, positions: readonly number[] = []): Segment[] {
  const hits = new Set(positions);
  const out: Segment[] = [];
  Array.from(text).forEach((ch, i) => {
    const hit = hits.has(i);
    const last = out[out.length - 1];
    if (last && last.hit === hit) last.text += ch;
    else out.push({ text: ch, hit });
  });
  return out;
}
