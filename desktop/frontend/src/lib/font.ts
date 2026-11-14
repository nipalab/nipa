import type { FontInfo } from "@/api/desktop";

export type CodeRange = { first: number; last: number };

export const codepoint = (cp: number): string => `U+${cp.toString(16).toUpperCase().padStart(4, "0")}`;

export const formatRange = (r: CodeRange): string => (r.first === r.last ? codepoint(r.first) : `${codepoint(r.first)}–${codepoint(r.last)}`);

const printable = (cp: number) => cp > 0x20 && !(cp >= 0x7f && cp <= 0xa0) && !(cp >= 0xd800 && cp <= 0xdfff);

// sampleChars returns up to limit printable characters of a range.
export function sampleChars(r: CodeRange, limit = 12): string {
  let out = "";
  for (let cp = r.first; cp <= r.last && out.length < limit * 2; cp++) {
    if (printable(cp)) out += String.fromCodePoint(cp);
    if ([...out].length >= limit) break;
  }
  return out;
}

export const rangeSize = (r: CodeRange): number => r.last - r.first + 1;

export type FontRow = { label: string; value: (f: FontInfo) => string | number };

export const fontRows: FontRow[] = [
  { label: "Family", value: (f) => f.family || "—" },
  { label: "Style", value: (f) => f.style || "—" },
  { label: "Version", value: (f) => f.version || "—" },
  { label: "Glyphs", value: (f) => f.glyphs },
  { label: "Characters", value: (f) => f.codepoints },
  { label: "Units per em", value: (f) => f.unitsPerEm },
  { label: "Ascent", value: (f) => f.ascent },
  { label: "Descent", value: (f) => f.descent },
  { label: "Line gap", value: (f) => f.lineGap },
  { label: "Cap height", value: (f) => f.capHeight },
  { label: "x-height", value: (f) => f.xHeight },
];
