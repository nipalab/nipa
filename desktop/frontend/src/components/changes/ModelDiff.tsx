import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Box, Columns2, Grid3x3, Layers, Loader2, Pause, Play, RotateCcw } from "lucide-react";
import { desktop, type FileDiff, type ModelCompare, type ModelSide, type ModelStats } from "@/api/desktop";
import { imageRefs, viewerKey } from "@/components/changes/ImageDiff";
import { MetaDiff } from "@/components/changes/Notices";
import { formatBytes } from "@/lib/diff";
import { clipName, disposeModel, loadModel, renderableFormats, sceneStats, type LoadedModel } from "@/lib/modelLoad";
import { createStage, type Shading, type Stage, type StageMode } from "@/lib/modelStage";
import { Group, Notice, ToolButton } from "@/components/changes/Toolbar";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { translate , locale } from "@/i18n";
import { withAbort } from "@/lib/abort";
import { mediaExpired } from "@/lib/media";

export function ModelDiff({ root, diff }: { root: string; diff: FileDiff }) {
  const { t } = useTranslation();
  const { old, next } = imageRefs(diff);
  const query = useQuery({
    queryKey: viewerKey(root, diff, "model"),
    queryFn: ({ signal }) => withAbort(signal, desktop.modelDiff(root, old, next)),
    staleTime: 30_000,
    // The result only holds media URLs; the files behind them are released
    // when the viewer closes, so cached results are not kept long.
    gcTime: 30_000,
    refetchOnWindowFocus: false,
  });
  const reloaded = useRef<string | null>(null);
  if (query.isPending) {
    return (
      <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> {t("modelDiff.loadingModel")}
      </div>
    );
  }
  if (query.isError) return <MetaDiff diff={diff} detail={translate("modelDiff.theModelCouldNotBe", { value: String((query.error as Error)?.message ?? query.error) })} />;
  if (!query.data || (!query.data.old && !query.data.new)) return <MetaDiff diff={diff} detail={translate("modelDiff.thereIsNoPreviewFor")} />;
  const data = query.data;
  const reload = () => {
    if (reloaded.current === diff.path) return false;
    reloaded.current = diff.path;
    void query.refetch();
    return true;
  };
  return <ModelCompareView key={`${data.old?.media ?? ""}:${data.new?.media ?? ""}`} compare={data} onExpired={reload} />;
}

type SideState = { model: LoadedModel | null; error: string | null; expired: boolean; loading: boolean };

const renderable = (side: ModelSide | null) => !!side && !side.tooLarge && !!side.url && renderableFormats.includes(side.format);

const errorText = (err: unknown) => (err instanceof Error ? err.message : String(err));

type MediaHooks = {
  hasMedia: (token: string) => Promise<boolean>;
  onExpired?: () => boolean;
};

// useLoadedSide fetches and parses one side. A load that fails because the
// prepared files were evicted reloads the comparison once (onExpired) and
// otherwise says the preview expired.
function useLoadedSide(side: ModelSide | null, { hasMedia, onExpired }: MediaHooks): SideState {
  const [state, setState] = useState<SideState>({ model: null, error: null, expired: false, loading: renderable(side) });
  const expiredRef = useRef(onExpired);
  expiredRef.current = onExpired;
  useEffect(() => {
    if (!side || !renderable(side)) {
      setState({ model: null, error: null, expired: false, loading: false });
      return;
    }
    const abort = new AbortController();
    setState({ model: null, error: null, expired: false, loading: true });
    const fail = (error: string, expired = false) => !abort.signal.aborted && setState({ model: null, error, expired, loading: false });
    loadModel(side, abort.signal).then(
      (model) => {
        if (abort.signal.aborted) disposeModel(model);
        else setState({ model, error: null, expired: false, loading: false });
      },
      (err: unknown) => {
        if (abort.signal.aborted) return;
        void mediaExpired([side.media], hasMedia).then((expired) => {
          // Aborted while asking: a reload would publish media no view releases.
          if (abort.signal.aborted) return;
          if (!expired) fail(errorText(err));
          else if (!expiredRef.current?.()) fail(errorText(err), true);
        });
      },
    );
    return () => abort.abort();
  }, [side, hasMedia]);
  return state;
}

type CompareProps = {
  compare: ModelCompare;
  hasMedia?: (token: string) => Promise<boolean>;
  release?: (tokens: string[]) => Promise<void>;
  // onExpired reloads the comparison when a prepared file was evicted; it
  // returns false when a reload was already tried.
  onExpired?: () => boolean;
};

export function ModelCompareView({ compare, hasMedia = desktop.hasMedia, release = desktop.releaseMedia, onExpired }: CompareProps) {
  const { t } = useTranslation();
  const { old, new: next } = compare;
  const tokens = [...(old?.tokens ?? []), ...(next?.tokens ?? [])].join(" ");
  useEffect(() => {
    if (!tokens) return;
    return () => {
      release(tokens.split(" ")).catch(() => undefined);
    };
  }, [tokens, release]);
  const oldState = useLoadedSide(old, { hasMedia, onExpired });
  const newState = useLoadedSide(next, { hasMedia, onExpired });
  const both = !!oldState.model && !!newState.model;
  const [mode, setMode] = useState<StageMode>("side");
  const [shading, setShading] = useState<Shading>("shaded");
  const [animation, setAnimation] = useState<string>("");
  const [playing, setPlaying] = useState(false);
  const [stageError, setStageError] = useState<string | null>(null);
  const stageRef = useRef<Stage | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);

  const oldStats = useMemo(() => old?.stats ?? (oldState.model ? sceneStats(oldState.model) : null), [old, oldState.model]);
  const newStats = useMemo(() => next?.stats ?? (newState.model ? sceneStats(newState.model) : null), [next, newState.model]);
  const clips = useMemo(
    () => [...new Set([oldState.model, newState.model].flatMap((m) => m?.animations.map((clip, i) => clipName(clip, i)) ?? []))],
    [oldState.model, newState.model],
  );

  useEffect(() => {
    const el = boxRef.current;
    if (!el) return;
    let stage: Stage;
    try {
      stage = createStage(el);
    } catch (err) {
      setStageError(err instanceof Error ? err.message : String(err));
      return;
    }
    stageRef.current = stage;
    const measure = () => stage.resize(el.clientWidth, el.clientHeight);
    measure();
    const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(measure);
    ro?.observe(el);
    return () => {
      ro?.disconnect();
      stage.dispose();
      stageRef.current = null;
    };
  }, []);

  useEffect(() => stageRef.current?.setModels(oldState.model, newState.model), [oldState.model, newState.model]);
  useEffect(() => stageRef.current?.setMode(mode), [mode]);
  useEffect(() => stageRef.current?.setShading(shading), [shading]);
  useEffect(() => stageRef.current?.playAnimation(animation || null), [animation]);
  useEffect(() => stageRef.current?.setPaused(!playing), [playing]);

  const anyRenderable = renderable(old) || renderable(next);
  const notices: ReactNode[] = [];
  for (const [label, side, state] of [
    [t("modelDiff.before"), old, oldState],
    [t("modelDiff.after"), next, newState],
  ] as const) {
    if (!side) continue;
    if (side.tooLarge) notices.push(<Notice key={`${label}-big`}>{t("modelDiff.tooLarge", { label, size: formatBytes(side.size) })}</Notice>);
    else if (!renderableFormats.includes(side.format)) notices.push(<Notice key={`${label}-fmt`}>{t("modelDiff.format", { label, format: side.format.toUpperCase() })}</Notice>);
    if (state.expired) notices.push(<Notice key={`${label}-err`}>{t("modelDiff.expired", { label })}</Notice>);
    else if (state.error) notices.push(<Notice key={`${label}-err`}>{t("modelDiff.error", { label, error: state.error })}</Notice>);
    if (side.missing?.length) notices.push(<Notice key={`${label}-missing`}>{t("modelDiff.missing", { label, files: side.missing.join(", ") })}</Notice>);
  }
  if (stageError) notices.push(<Notice key="stage">{t("modelDiff.noStage", { error: stageError })}</Notice>);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-10 shrink-0 items-center gap-2 overflow-x-auto border-b border-border px-3 text-xs">
        {both && (
          <Group label={t("modelDiff.compareMode")}>
            <ToolButton active={mode === "side"} label={t("modelDiff.sideBySide")} onClick={() => setMode("side")}>
              <Columns2 className="size-3.5" />
            </ToolButton>
            <ToolButton active={mode === "overlay"} label={t("modelDiff.overlay")} onClick={() => setMode("overlay")}>
              <Layers className="size-3.5" />
            </ToolButton>
          </Group>
        )}
        <Group label={t("modelDiff.shading")}>
          <ToolButton active={shading === "shaded"} label={t("modelDiff.shaded")} onClick={() => setShading("shaded")}>
            <Box className="size-3.5" />
          </ToolButton>
          <ToolButton active={shading === "wireframe"} label={t("modelDiff.wireframe")} onClick={() => setShading("wireframe")}>
            <Grid3x3 className="size-3.5" />
          </ToolButton>
          <ToolButton active={shading === "both"} label={t("modelDiff.shadedWithWireframe")} onClick={() => setShading("both")}>
            <span className="font-mono text-[10px]">{t("modelDiff.sW")}</span>
          </ToolButton>
        </Group>
        {clips.length > 0 && (
          <div className="flex items-center gap-1">
            <select aria-label={t("modelDiff.animation")} value={animation} onChange={(e) => setAnimation(e.target.value)} className="h-6 rounded border border-border bg-card px-1 text-foreground">
              <option value="">{t("modelDiff.bindPose")}</option>
              {clips.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
            <ToolButton label={playing ? translate("modelDiff.pause") : translate("modelDiff.play")} onClick={() => setPlaying((p) => !p)}>
              {playing ? <Pause className="size-3.5" /> : <Play className="size-3.5" />}
            </ToolButton>
          </div>
        )}
        <div className="ml-auto">
          <ToolButton label={t("modelDiff.resetView")} onClick={() => stageRef.current?.resetView()}>
            <RotateCcw className="size-3.5" />
          </ToolButton>
        </div>
      </div>
      <div className="relative min-h-0 flex-1 bg-card">
        <div ref={boxRef} className="absolute inset-0" data-testid="model-stage" />
        {both && mode === "side" && (
          <>
            <PaneLabel className="left-2">{t("modelDiff.before")}</PaneLabel>
            <PaneLabel className="left-[calc(50%+0.5rem)]">{t("modelDiff.after")}</PaneLabel>
            <div className="pointer-events-none absolute inset-y-0 left-1/2 w-px bg-border" />
          </>
        )}
        {both && mode === "overlay" && <PaneLabel className="left-2">{t("modelDiff.afterBeforeAsOrange")}</PaneLabel>}
        {!both && (oldState.model || newState.model) && <PaneLabel className="left-2">{newState.model ? (old ? translate("modelDiff.after") : translate("modelDiff.added")) : next ? translate("modelDiff.before") : translate("modelDiff.deleted")}</PaneLabel>}
        {(oldState.loading || newState.loading) && (
          <div className="absolute inset-0 flex items-center justify-center gap-2 text-xs text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> {t("modelDiff.buildingScene")}
          </div>
        )}
        {!anyRenderable && <div className="absolute inset-0 flex items-center justify-center text-xs text-muted-foreground">{t("modelDiff.no3DPreviewForThis")}</div>}
      </div>
      <div className="flex max-h-48 shrink-0 flex-wrap items-start gap-x-8 gap-y-2 overflow-auto border-t border-border px-4 py-2 text-[11px]">
        <StatsTable old={old} next={next} oldStats={oldStats} newStats={newStats} />
        <NameChanges title={t("modelDiff.materials")} old={oldStats?.materials} next={newStats?.materials} />
        <NameChanges title={t("modelDiff.animations")} old={oldStats?.animations} next={newStats?.animations} />
        {notices.length > 0 && <div className="flex flex-col gap-1">{notices}</div>}
      </div>
    </div>
  );
}

function PaneLabel({ className, children }: { className: string; children: ReactNode }) {
  return <span className={cn("pointer-events-none absolute top-2 rounded bg-background/80 px-1.5 py-0.5 text-[11px] font-medium shadow-sm", className)}>{children}</span>;
}

type StatRow = { label: string; value: (s: ModelStats) => number };

const statRows: StatRow[] = [
  { label: translate("modelDiff.vertices"), value: (s) => s.vertices },
  { label: translate("modelDiff.triangles"), value: (s) => s.triangles },
  { label: translate("modelDiff.meshes"), value: (s) => s.meshes },
  { label: translate("modelDiff.materials"), value: (s) => s.materials?.length ?? 0 },
  { label: translate("modelDiff.textures"), value: (s) => s.textures },
  { label: translate("modelDiff.bones"), value: (s) => s.joints },
  { label: translate("modelDiff.animations"), value: (s) => s.animations?.length ?? 0 },
  { label: translate("modelDiff.morphTargets"), value: (s) => s.morphTargets },
];

export function boundsSize(s: ModelStats | null): string {
  if (!s?.min || !s.max) return "—";
  return s.max.map((v, i) => (v - s.min![i]).toFixed(2)).join(" × ");
}

function Delta({ value }: { value: number }) {
  if (value === 0) return null;
  return <span className={value > 0 ? "text-success" : "text-danger"}>{value > 0 ? translate("modelDiff.text", { toLocaleString: value.toLocaleString(locale()) }) : translate("modelDiff.text2", { toLocaleString: (-value).toLocaleString(locale()) })}</span>;
}

function StatsTable({ old, next, oldStats, newStats }: { old: ModelSide | null; next: ModelSide | null; oldStats: ModelStats | null; newStats: ModelStats | null }) {
  const { t } = useTranslation();
  const both = !!oldStats && !!newStats;
  const cell = (s: ModelStats | null, f: (s: ModelStats) => string) => (s ? f(s) : "—");
  const generator = oldStats?.generator || newStats?.generator;
  return (
    <table aria-label={t("modelDiff.modelDetails")}>
      <thead>
        <tr className="text-muted-foreground">
          <th />
          <th className="px-3 text-left font-medium">{t("modelDiff.before")}</th>
          <th className="px-3 text-left font-medium">{t("modelDiff.after")}</th>
          {both && <th className="px-3 text-left font-medium">{t("modelDiff.change")}</th>}
        </tr>
      </thead>
      <tbody className="font-mono">
        <tr>
          <th className="pr-3 text-left font-sans font-normal text-muted-foreground">{t("modelDiff.fileSize")}</th>
          <td className="px-3">{old ? formatBytes(old.size) : "—"}</td>
          <td className="px-3">{next ? formatBytes(next.size) : "—"}</td>
          {both && <td />}
        </tr>
        {statRows.map((r) => (
          <tr key={r.label}>
            <th className="pr-3 text-left font-sans font-normal text-muted-foreground">{r.label}</th>
            <td className="px-3">{cell(oldStats, (s) => r.value(s).toLocaleString(locale()))}</td>
            <td className="px-3">{cell(newStats, (s) => r.value(s).toLocaleString(locale()))}</td>
            {both && (
              <td className="px-3">
                <Delta value={r.value(newStats!) - r.value(oldStats!)} />
              </td>
            )}
          </tr>
        ))}
        <tr>
          <th className="pr-3 text-left font-sans font-normal text-muted-foreground">{t("modelDiff.bounds")}</th>
          <td className="px-3">{boundsSize(oldStats)}</td>
          <td className={cn("px-3", both && boundsSize(oldStats) !== boundsSize(newStats) && "font-semibold text-warning")}>{boundsSize(newStats)}</td>
          {both && <td />}
        </tr>
        {generator && (
          <tr>
            <th className="pr-3 text-left font-sans font-normal text-muted-foreground">{t("modelDiff.exportedBy")}</th>
            <td className="px-3">{oldStats?.generator || "—"}</td>
            <td className="px-3">{newStats?.generator || "—"}</td>
            {both && <td />}
          </tr>
        )}
      </tbody>
    </table>
  );
}

function NameChanges({ title, old, next }: { title: string; old?: string[] | null; next?: string[] | null }) {
  if (!old || !next) return null;
  const added = next.filter((n) => !old.includes(n));
  const removed = old.filter((n) => !next.includes(n));
  if (added.length === 0 && removed.length === 0) return null;
  return (
    <div>
      <p className="mb-1 text-muted-foreground">{title}</p>
      <ul aria-label={translate("modelDiff.changes", { title })}>
        {added.map((n) => (
          <li key={`+${n}`} className="text-success">
            + {n}
          </li>
        ))}
        {removed.map((n) => (
          <li key={`-${n}`} className="text-danger">
            − {n}
          </li>
        ))}
      </ul>
    </div>
  );
}
