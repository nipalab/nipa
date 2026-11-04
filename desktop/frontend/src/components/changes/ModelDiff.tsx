import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Box, Columns2, Grid3x3, Layers, Loader2, Pause, Play, RotateCcw } from "lucide-react";
import { desktop, type FileDiff, type ModelCompare, type ModelSide, type ModelStats } from "@/api/desktop";
import { imageRefs, viewerKey } from "@/components/changes/ImageDiff";
import { MetaDiff } from "@/components/changes/Notices";
import { formatBytes } from "@/lib/diff";
import { clipName, loadModel, renderableFormats, sceneStats, type LoadedModel } from "@/lib/modelLoad";
import { createStage, type Shading, type Stage, type StageMode } from "@/lib/modelStage";
import { Group, Notice, ToolButton } from "@/components/changes/Toolbar";
import { cn } from "@/lib/utils";

export function ModelDiff({ root, diff }: { root: string; diff: FileDiff }) {
  const { old, next } = imageRefs(diff);
  const query = useQuery({
    queryKey: viewerKey(root, diff, "model"),
    queryFn: () => desktop.modelDiff(root, old, next),
    staleTime: 30_000,
  });
  if (query.isPending) {
    return (
      <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> Loading model…
      </div>
    );
  }
  if (query.isError) return <MetaDiff diff={diff} detail={`The model could not be read: ${String((query.error as Error)?.message ?? query.error)}`} />;
  if (!query.data || (!query.data.old && !query.data.new)) return <MetaDiff diff={diff} detail="There is no preview for this model." />;
  return <ModelCompareView compare={query.data} />;
}

type SideState = { model: LoadedModel | null; error: string | null; loading: boolean };

const renderable = (side: ModelSide | null) => !!side && !side.tooLarge && !!side.data && renderableFormats.includes(side.format);

function useLoadedSide(side: ModelSide | null): SideState {
  const [state, setState] = useState<SideState>({ model: null, error: null, loading: renderable(side) });
  useEffect(() => {
    if (!side || !renderable(side)) {
      setState({ model: null, error: null, loading: false });
      return;
    }
    let live = true;
    setState({ model: null, error: null, loading: true });
    loadModel(side).then(
      (model) => live && setState({ model, error: null, loading: false }),
      (err: unknown) => live && setState({ model: null, error: err instanceof Error ? err.message : String(err), loading: false }),
    );
    return () => {
      live = false;
    };
  }, [side]);
  return state;
}

export function ModelCompareView({ compare }: { compare: ModelCompare }) {
  const { old, new: next } = compare;
  const oldState = useLoadedSide(old);
  const newState = useLoadedSide(next);
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
    ["Before", old, oldState],
    ["After", next, newState],
  ] as const) {
    if (!side) continue;
    if (side.tooLarge) notices.push(<Notice key={`${label}-big`}>{label}: the model and its files are too large to show ({formatBytes(side.size)}); only statistics are compared.</Notice>);
    else if (!renderableFormats.includes(side.format)) notices.push(<Notice key={`${label}-fmt`}>{label}: {side.format.toUpperCase()} models cannot be shown yet.</Notice>);
    if (state.error) notices.push(<Notice key={`${label}-err`}>{label}: {state.error}</Notice>);
    if (side.missing?.length) notices.push(<Notice key={`${label}-missing`}>{label}: missing {side.missing.join(", ")}</Notice>);
  }
  if (stageError) notices.push(<Notice key="stage">3D preview is not available here: {stageError}</Notice>);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-10 shrink-0 items-center gap-2 overflow-x-auto border-b border-border px-3 text-xs">
        {both && (
          <Group label="Compare mode">
            <ToolButton active={mode === "side"} label="Side by side" onClick={() => setMode("side")}>
              <Columns2 className="size-3.5" />
            </ToolButton>
            <ToolButton active={mode === "overlay"} label="Overlay" onClick={() => setMode("overlay")}>
              <Layers className="size-3.5" />
            </ToolButton>
          </Group>
        )}
        <Group label="Shading">
          <ToolButton active={shading === "shaded"} label="Shaded" onClick={() => setShading("shaded")}>
            <Box className="size-3.5" />
          </ToolButton>
          <ToolButton active={shading === "wireframe"} label="Wireframe" onClick={() => setShading("wireframe")}>
            <Grid3x3 className="size-3.5" />
          </ToolButton>
          <ToolButton active={shading === "both"} label="Shaded with wireframe" onClick={() => setShading("both")}>
            <span className="font-mono text-[10px]">S+W</span>
          </ToolButton>
        </Group>
        {clips.length > 0 && (
          <div className="flex items-center gap-1">
            <select aria-label="Animation" value={animation} onChange={(e) => setAnimation(e.target.value)} className="h-6 rounded border border-border bg-card px-1 text-foreground">
              <option value="">Bind pose</option>
              {clips.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
            <ToolButton label={playing ? "Pause" : "Play"} onClick={() => setPlaying((p) => !p)}>
              {playing ? <Pause className="size-3.5" /> : <Play className="size-3.5" />}
            </ToolButton>
          </div>
        )}
        <div className="ml-auto">
          <ToolButton label="Reset view" onClick={() => stageRef.current?.resetView()}>
            <RotateCcw className="size-3.5" />
          </ToolButton>
        </div>
      </div>
      <div className="relative min-h-0 flex-1 bg-card">
        <div ref={boxRef} className="absolute inset-0" data-testid="model-stage" />
        {both && mode === "side" && (
          <>
            <PaneLabel className="left-2">Before</PaneLabel>
            <PaneLabel className="left-[calc(50%+0.5rem)]">After</PaneLabel>
            <div className="pointer-events-none absolute inset-y-0 left-1/2 w-px bg-border" />
          </>
        )}
        {both && mode === "overlay" && <PaneLabel className="left-2">After · Before as orange wireframe</PaneLabel>}
        {!both && (oldState.model || newState.model) && <PaneLabel className="left-2">{newState.model ? (old ? "After" : "Added") : next ? "Before" : "Deleted"}</PaneLabel>}
        {(oldState.loading || newState.loading) && (
          <div className="absolute inset-0 flex items-center justify-center gap-2 text-xs text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> Building scene…
          </div>
        )}
        {!anyRenderable && <div className="absolute inset-0 flex items-center justify-center text-xs text-muted-foreground">No 3D preview for this model.</div>}
      </div>
      <div className="flex max-h-48 shrink-0 flex-wrap items-start gap-x-8 gap-y-2 overflow-auto border-t border-border px-4 py-2 text-[11px]">
        <StatsTable old={old} next={next} oldStats={oldStats} newStats={newStats} />
        <NameChanges title="Materials" old={oldStats?.materials} next={newStats?.materials} />
        <NameChanges title="Animations" old={oldStats?.animations} next={newStats?.animations} />
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
  { label: "Vertices", value: (s) => s.vertices },
  { label: "Triangles", value: (s) => s.triangles },
  { label: "Meshes", value: (s) => s.meshes },
  { label: "Materials", value: (s) => s.materials?.length ?? 0 },
  { label: "Textures", value: (s) => s.textures },
  { label: "Bones", value: (s) => s.joints },
  { label: "Animations", value: (s) => s.animations?.length ?? 0 },
  { label: "Morph targets", value: (s) => s.morphTargets },
];

export function boundsSize(s: ModelStats | null): string {
  if (!s?.min || !s.max) return "—";
  return s.max.map((v, i) => (v - s.min![i]).toFixed(2)).join(" × ");
}

function Delta({ value }: { value: number }) {
  if (value === 0) return null;
  return <span className={value > 0 ? "text-success" : "text-danger"}>{value > 0 ? `+${value.toLocaleString()}` : `−${(-value).toLocaleString()}`}</span>;
}

function StatsTable({ old, next, oldStats, newStats }: { old: ModelSide | null; next: ModelSide | null; oldStats: ModelStats | null; newStats: ModelStats | null }) {
  const both = !!oldStats && !!newStats;
  const cell = (s: ModelStats | null, f: (s: ModelStats) => string) => (s ? f(s) : "—");
  const generator = oldStats?.generator || newStats?.generator;
  return (
    <table aria-label="Model details">
      <thead>
        <tr className="text-muted-foreground">
          <th />
          <th className="px-3 text-left font-medium">Before</th>
          <th className="px-3 text-left font-medium">After</th>
          {both && <th className="px-3 text-left font-medium">Change</th>}
        </tr>
      </thead>
      <tbody className="font-mono">
        <tr>
          <th className="pr-3 text-left font-sans font-normal text-muted-foreground">File size</th>
          <td className="px-3">{old ? formatBytes(old.size) : "—"}</td>
          <td className="px-3">{next ? formatBytes(next.size) : "—"}</td>
          {both && <td />}
        </tr>
        {statRows.map((r) => (
          <tr key={r.label}>
            <th className="pr-3 text-left font-sans font-normal text-muted-foreground">{r.label}</th>
            <td className="px-3">{cell(oldStats, (s) => r.value(s).toLocaleString())}</td>
            <td className="px-3">{cell(newStats, (s) => r.value(s).toLocaleString())}</td>
            {both && (
              <td className="px-3">
                <Delta value={r.value(newStats!) - r.value(oldStats!)} />
              </td>
            )}
          </tr>
        ))}
        <tr>
          <th className="pr-3 text-left font-sans font-normal text-muted-foreground">Bounds</th>
          <td className="px-3">{boundsSize(oldStats)}</td>
          <td className={cn("px-3", both && boundsSize(oldStats) !== boundsSize(newStats) && "font-semibold text-warning")}>{boundsSize(newStats)}</td>
          {both && <td />}
        </tr>
        {generator && (
          <tr>
            <th className="pr-3 text-left font-sans font-normal text-muted-foreground">Exported by</th>
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
      <ul aria-label={`${title} changes`}>
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
