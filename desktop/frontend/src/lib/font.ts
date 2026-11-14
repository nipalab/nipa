import type { FontInfo } from "@/api/desktop";
import { translate } from "@/i18n";

export type CodeRange = { first: number; last: number };

export const codepoint = (cp: number): string => translate("font.u", { value: cp.toString(16).toUpperCase().padStart(4, "0") });

export const formatRange = (r: CodeRange): string => (r.first === r.last ? codepoint(r.first) : translate("font.text", { value: codepoint(r.first), value2: codepoint(r.last) }));

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
  { label: translate("font.family"), value: (f) => f.family || "—" },
  { label: translate("font.style"), value: (f) => f.style || "—" },
  { label: translate("font.version"), value: (f) => f.version || "—" },
  { label: translate("font.glyphs"), value: (f) => f.glyphs },
  { label: translate("font.characters"), value: (f) => f.codepoints },
  { label: translate("font.unitsPerEm"), value: (f) => f.unitsPerEm },
  { label: translate("font.ascent"), value: (f) => f.ascent },
  { label: translate("font.descent"), value: (f) => f.descent },
  { label: translate("font.lineGap"), value: (f) => f.lineGap },
  { label: translate("font.capHeight"), value: (f) => f.capHeight },
  { label: "x-height", value: (f) => f.xHeight },
];
