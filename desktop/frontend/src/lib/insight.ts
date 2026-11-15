import type { AssetReferences, ChangeSet, Origin, OriginInput } from "@/api/desktop";
import { plural, translate , locale } from "@/i18n";

export const REFERRER_PREVIEW = 3;

export { plural };

// referrerSummary names how many files use an asset and the first few of them.
export function referrerSummary(usedBy: string[], max = REFERRER_PREVIEW): string {
  if (usedBy.length === 0) return "";
  const shown = usedBy.slice(0, max).join(", ");
  const more = usedBy.length > max ? translate("insight.andMore", { max: usedBy.length - max }) : "";
  return translate("insight.usedBy", { value: plural(usedBy.length, "file"), shown, more });
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
  recorded: translate("insight.theFileMatchesTheRecorded"),
  modified: translate("insight.theFileChangedAfterIt"),
  edited: translate("insight.aPersonEditedTheFile"),
  missing: translate("insight.noOriginRecord"),
};

export const sourceNames: Record<string, string> = {
  "png-text": translate("insight.pNGMetadata"),
  c2pa: translate("insight.c2PAManifestNotVerified"),
  manual: translate("insight.enteredByHand"),
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
  if (!form.tool.trim()) return translate("insight.enterTheToolThatMade");
  if (form.seed.trim() && !/^\d+$/.test(form.seed.trim())) return translate("insight.theSeedMustBeA");
  return "";
}

export function recordedDate(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? iso
    : d.toLocaleDateString(locale(), {
        year: "numeric",
        month: "short",
        day: "numeric",
      });
}
