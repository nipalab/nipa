import { useState, type FormEvent, type ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ChevronDown, ChevronRight, FileQuestion, Link2, Loader2, Sparkles } from "lucide-react";
import { desktop, type AssetReferences, type Origin } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { useAssetRefs, useInsightRefresh, useOrigin } from "@/hooks/useAssetInsight";
import { emptyOriginForm, originStatusText, plural, recordedDate, sourceNames, unresolvedRefs, validateOrigin, type OriginForm } from "@/lib/insight";
import { cn, errorMessage, readSetting, writeSetting } from "@/lib/utils";

type InsightProps = {
  root: string;
  path: string;
  onOpenFile?: (path: string) => void;
  className?: string;
};

// AssetInsight shows where an engine asset is used and where a file came
// from. It renders nothing for files that are neither an engine asset nor
// covered by a provenance record or rule.
export function AssetInsight({ root, path, onOpenFile, className }: InsightProps) {
  useInsightRefresh(root);
  const refs = useAssetRefs(root, path);
  const origin = useOrigin(root, path);
  const showRefs = refs.isPending || refs.isError || !!refs.data?.asset;
  const showOrigin = !!origin.data?.origin;
  if (!showRefs && !showOrigin) return null;
  return (
    <div className={cn("space-y-3", className)}>
      {showRefs && <References refs={refs.data} loading={refs.isPending} error={refs.isError ? errorMessage(refs.error) : ""} onOpenFile={onOpenFile} />}
      {showOrigin && <OriginSection root={root} path={path} origin={origin.data!.origin!} enabled={origin.data!.enabled} />}
    </div>
  );
}

const PANEL_KEY = "nipa.insightPanel";

// InsightPanel is the collapsible footer of the Changes screen around
// AssetInsight; it stays out of the way for files it has nothing to say about.
export function InsightPanel({ root, path, onOpenFile }: InsightProps) {
  useInsightRefresh(root);
  const [open, setOpenState] = useState(() => readSetting(PANEL_KEY) === "open");
  const refs = useAssetRefs(root, path);
  const origin = useOrigin(root, path);
  if (!refs.data?.asset && !origin.data?.origin) return null;
  const setOpen = (next: boolean) => {
    writeSetting(PANEL_KEY, next ? "open" : null);
    setOpenState(next);
  };
  const Chevron = open ? ChevronDown : ChevronRight;
  const parts: string[] = [];
  if (refs.data?.asset) parts.push(`Used by ${refs.data.usedBy.length}`, `Uses ${refs.data.uses.filter((r) => r.resolved).length}`);
  if (origin.data?.origin) parts.push(`Origin: ${origin.data.origin.status}`);
  return (
    <section aria-label="References and origin" className="shrink-0 border-t border-border bg-card">
      <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[11px]">
        <Chevron className="size-3.5 text-muted-foreground" />
        <span className="font-medium">References &amp; origin</span>
        <span className="truncate text-muted-foreground">{parts.join(" · ")}</span>
        {origin.data?.origin?.status === "missing" && <AlertTriangle className="size-3.5 shrink-0 text-warning" aria-label="Origin record missing" />}
      </button>
      {open && (
        <div className="max-h-64 overflow-y-auto px-4 pb-3">
          <AssetInsight root={root} path={path} onOpenFile={onOpenFile} />
        </div>
      )}
    </section>
  );
}

function References({ refs, loading, error, onOpenFile }: { refs?: AssetReferences; loading: boolean; error: string; onOpenFile?: (path: string) => void }) {
  return (
    <section aria-label="References">
      <Heading icon={<Link2 className="size-3.5" />}>References</Heading>
      {loading ? (
        <p className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <Loader2 className="size-3 animate-spin" /> Scanning references…
        </p>
      ) : error ? (
        <p className="text-[11px] text-muted-foreground">References are not available: {error}</p>
      ) : refs ? (
        <div className="space-y-2 text-[12px]">
          <Group title={`Used by (${refs.usedBy.length})`}>
            {refs.usedBy.length === 0 ? (
              <p className="text-[11px] text-muted-foreground">
                Not referenced anywhere
                {refs.partial || refs.truncated ? " that was scanned" : ""}.
              </p>
            ) : (
              <PathList paths={refs.usedBy} onOpenFile={onOpenFile} />
            )}
          </Group>
          <ResolvedUses refs={refs} onOpenFile={onOpenFile} />
          <Unresolved refs={refs} />
          {refs.truncated && <Note>The scan stopped after {plural(refs.scanned, "file")}, so these lists may be incomplete.</Note>}
          {refs.partial && <Note>This clone only downloads some folders. Files outside them are not scanned, so references may be missing.</Note>}
        </div>
      ) : null}
    </section>
  );
}

function ResolvedUses({ refs, onOpenFile }: { refs: AssetReferences; onOpenFile?: (path: string) => void }) {
  const uses = [...new Set(refs.uses.filter((r) => r.resolved && r.path).map((r) => r.path))];
  if (uses.length === 0) return null;
  return (
    <Group title={`Uses (${uses.length})`}>
      <PathList paths={uses} onOpenFile={onOpenFile} />
    </Group>
  );
}

function Unresolved({ refs }: { refs: AssetReferences }) {
  const list = unresolvedRefs(refs);
  if (list.length === 0) return null;
  return (
    <Group title={`Unresolved (${list.length})`}>
      <ul className="space-y-0.5">
        {list.map((r) => (
          <li key={`${r.raw}:${r.path}`} className="flex items-center gap-1.5 text-[11px]">
            <FileQuestion className="size-3 shrink-0 text-muted-foreground" />
            <span className="min-w-0 truncate font-mono" title={r.raw}>
              {r.path || r.raw}
            </span>
            <span className="shrink-0 text-muted-foreground">{r.outside ? "outside this clone" : "not found"}</span>
          </li>
        ))}
      </ul>
    </Group>
  );
}

function PathList({ paths, onOpenFile }: { paths: string[]; onOpenFile?: (path: string) => void }) {
  return (
    <ul className="space-y-0.5">
      {paths.map((p) => (
        <li key={p}>
          {onOpenFile ? (
            <button
              type="button"
              className="max-w-full truncate rounded px-1 text-left font-mono text-[11px] hover:bg-muted hover:underline"
              title={`Show ${p} in Files`}
              onClick={() => onOpenFile(p)}
            >
              {p}
            </button>
          ) : (
            <span className="block truncate px-1 font-mono text-[11px]">{p}</span>
          )}
        </li>
      ))}
    </ul>
  );
}

function OriginSection({ root, path, origin, enabled }: { root: string; path: string; origin: Origin; enabled: boolean }) {
  const [adding, setAdding] = useState(false);
  const missing = origin.status === "missing";
  return (
    <section aria-label="Origin">
      <Heading icon={<Sparkles className="size-3.5" />}>Origin</Heading>
      {missing ? (
        <div className="space-y-2">
          <p className="flex items-start gap-1.5 text-[11px] text-warning">
            <AlertTriangle className="mt-px size-3.5 shrink-0" />
            <span>{origin.required ? "A rule in .nipaassets.yml requires an origin record for this file, and it has none." : "No origin record."}</span>
          </p>
          {adding ? (
            <OriginEditor root={root} path={path} enabled={enabled} onDone={() => setAdding(false)} />
          ) : (
            <Button size="sm" onClick={() => setAdding(true)} disabled={!origin.exists} title={origin.exists ? undefined : "The file is not on disk"}>
              Add record…
            </Button>
          )}
        </div>
      ) : (
        <div className="space-y-1.5 text-[12px]">
          <p className={cn("text-[11px]", origin.status === "recorded" ? "text-muted-foreground" : "text-warning")}>{originStatusText[origin.status] ?? origin.status}</p>
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
            <Field label="Tool" value={origin.tool} />
            <Field label="Model" value={origin.model} />
            <Field label="Seed" value={origin.seed} mono />
            <Field label="Source" value={sourceNames[origin.source] ?? origin.source} />
            <Field label="Recorded" value={recordedDate(origin.recordedAt)} />
            <Field label="License" value={origin.license} />
            <Field label="Note" value={origin.note} />
          </dl>
        </div>
      )}
    </section>
  );
}

function OriginEditor({ root, path, enabled, onDone }: { root: string; path: string; enabled: boolean; onDone: () => void }) {
  const queryClient = useQueryClient();
  const [form, setForm] = useState<OriginForm>(() => emptyOriginForm());
  const [problem, setProblem] = useState("");
  const save = useMutation({
    mutationFn: () => desktop.setProvenance(root, { path, ...form }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["provenance", root] });
      void queryClient.invalidateQueries({ queryKey: ["changes", root] });
      onDone();
    },
    onError: (err: unknown) => setProblem(errorMessage(err)),
  });
  const set = (key: keyof OriginForm) => (value: string | boolean) => setForm((f) => ({ ...f, [key]: value }));
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const invalid = validateOrigin(form);
    setProblem(invalid);
    if (!invalid) save.mutate();
  };
  return (
    <form aria-label="Add origin record" onSubmit={submit} className="space-y-2 rounded-md border border-border p-3 text-[12px]">
      <Input label="Tool" value={form.tool} onChange={set("tool")} placeholder="ComfyUI, Firefly, ElevenLabs…" />
      <Input label="Model" value={form.model} onChange={set("model")} />
      <Input label="Seed" value={form.seed} onChange={set("seed")} inputMode="numeric" />
      <Input label="License" value={form.license} onChange={set("license")} />
      <label className="block space-y-1">
        <span className="text-muted-foreground">Prompt</span>
        <textarea value={form.prompt} onChange={(e) => set("prompt")(e.target.value)} rows={2} className="w-full rounded-md border border-border bg-background px-2 py-1 text-[12px]" />
        <span className="block text-[11px] text-muted-foreground">Only a fingerprint of the prompt is stored, never the text.</span>
      </label>
      <Input label="Note" value={form.note} onChange={set("note")} />
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={form.humanEdited} onChange={(e) => set("humanEdited")(e.target.checked)} />
        <span>Edited by a person after it was generated</span>
      </label>
      {!enabled && <p className="text-[11px] text-muted-foreground">This creates .nipaprovenance.yml and stages it, which turns on origin tracking for the project.</p>}
      {problem && (
        <p role="alert" className="text-[11px] text-danger">
          {problem}
        </p>
      )}
      <div className="flex justify-end gap-2">
        <Button size="sm" onClick={onDone} disabled={save.isPending}>
          Cancel
        </Button>
        <Button size="sm" variant="primary" type="submit" disabled={save.isPending}>
          {save.isPending && <Loader2 className="animate-spin" />}
          Save record
        </Button>
      </div>
    </form>
  );
}

function Input({ label, value, onChange, placeholder, inputMode }: { label: string; value: string; onChange: (v: string) => void; placeholder?: string; inputMode?: "numeric" }) {
  return (
    <label className="block space-y-1">
      <span className="text-muted-foreground">{label}</span>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        inputMode={inputMode}
        className="h-7 w-full rounded-md border border-border bg-background px-2 text-[12px]"
      />
    </label>
  );
}

function Field({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  if (!value) return null;
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={cn("min-w-0 select-text break-words", mono && "font-mono text-[11px]")}>{value}</dd>
    </>
  );
}

function Heading({ icon, children }: { icon: ReactNode; children: ReactNode }) {
  return (
    <h3 className="mb-1.5 flex items-center gap-1.5 text-[12px] font-semibold">
      {icon}
      {children}
    </h3>
  );
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div>
      <h4 className="mb-0.5 text-[11px] font-medium text-muted-foreground">{title}</h4>
      {children}
    </div>
  );
}

function Note({ children }: { children: ReactNode }) {
  return <p className="flex items-start gap-1.5 text-[11px] text-muted-foreground">{children}</p>;
}
