import type { AssetReferences, ChangeSet, Origin, OriginInput } from "@/api/desktop";

export const REFERRER_PREVIEW = 3;

export const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

// referrerSummary names how many files use an asset and the first few of them.
export function referrerSummary(usedBy: string[], max = REFERRER_PREVIEW): string {
  if (usedBy.length === 0) return "";
  const shown = usedBy.slice(0, max).join(", ");
  const more = usedBy.length > max ? ` and ${usedBy.length - max} more` : "";
  return `Used by ${plural(usedBy.length, "file")}: ${shown}${more}.`;
}

// unresolvedRefs lists the references the graph could not map to a file:
// broken ones in this clone first, then those outside a sparse checkout.
export function unresolvedRefs(refs: AssetReferences) {
  return [...refs.uses.filter((r) => !r.resolved).map((r) => ({ ...r, outside: false })), ...refs.unknown.map((r) => ({ ...r, outside: true }))];
}

// deletionTargets returns the paths among paths that staging would record as
// deletions (files missing from disk).
export function deletionTargets(changes: ChangeSet, paths: string[]): string[] {
  const missing = new Set(changes.unstaged.filter((f) => f.status === "deleted").map((f) => f.path));
  return paths.filter((p) => missing.has(p));
}

export const originStatusText: Record<string, string> = {
  recorded: "The file matches the recorded version.",
  modified: "The file changed after it was recorded and is not marked as edited by a person.",
  edited: "A person edited the file after it was generated.",
  missing: "No origin record.",
};

export const sourceNames: Record<string, string> = {
  "png-text": "PNG metadata",
  c2pa: "C2PA manifest (not verified)",
  manual: "Entered by hand",
};

export type OriginForm = Omit<OriginInput, "path">;

export const emptyOriginForm = (origin?: Origin): OriginForm => ({
  tool: origin?.tool ?? "",
  model: origin?.model ?? "",
  seed: origin?.seed ?? "",
  license: origin?.license ?? "",
  prompt: "",
  note: origin?.note ?? "",
  humanEdited: origin?.humanEdited ?? false,
});

// validateOrigin mirrors the service checks so the form can explain them
// before anything is sent.
export function validateOrigin(form: OriginForm): string {
  if (!form.tool.trim()) return "Enter the tool that made the file.";
  if (form.seed.trim() && !/^\d+$/.test(form.seed.trim())) return "The seed must be a whole number.";
  return "";
}

export function recordedDate(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? iso
    : d.toLocaleDateString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
      });
}
