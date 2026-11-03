import { useEffect, useMemo, useRef, useState, type CSSProperties, type PointerEvent, type ReactNode, type RefObject, type WheelEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { Columns2, Flame, Layers, Loader2, Maximize, Repeat, SquareSplitHorizontal, ZoomIn, ZoomOut } from "lucide-react";
import { desktop, type FileDiff, type ImageCompare, type ImageInfo, type ImageRef, type ImageStats } from "@/api/desktop";
import { MetaDiff } from "@/components/changes/Notices";
import { formatBytes } from "@/lib/diff";
import { allChannels, clampZoom, fitZoom, formatPercent, heatmap, isolateChannels, stepZoom, type Channels, type Pixels } from "@/lib/imageCompare";
import { drawPixels, loadPixels } from "@/lib/pixels";
import { cn } from "@/lib/utils";

export type CompareMode = "side" | "swipe" | "onion" | "difference" | "blink";

const modes: { id: CompareMode; label: string; Icon: typeof Columns2 }[] = [
  { id: "side", label: "Side by side", Icon: Columns2 },
  { id: "swipe", label: "Swipe", Icon: SquareSplitHorizontal },
  { id: "onion", label: "Onion skin", Icon: Layers },
  { id: "difference", label: "Difference", Icon: Flame },
  { id: "blink", label: "Blink", Icon: Repeat },
];

const modeKey = "nipa.imageMode";
const blinkMs = 700;

// imageRefs maps a working-copy change onto the two image versions: the last
// synced snapshot before, the working file after.
export function imageRefs(diff: FileDiff): { old: ImageRef | null; next: ImageRef | null } {
  return {
    old: diff.old ? { source: "snapshot", path: diff.old.path || diff.oldPath || diff.path } : null,
    next: diff.new ? { source: "working", path: diff.new.path || diff.path } : null,
  };
}

export function ImageDiff({ root, diff }: { root: string; diff: FileDiff }) {
  const { old, next } = imageRefs(diff);
  const query = useQuery({
    queryKey: ["diff", root, diff.path, "image"],
    queryFn: () => desktop.imageDiff(root, old, next),
    staleTime: 30_000,
  });
  if (query.isPending) {
    return (
      <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> Decoding image…
      </div>
    );
  }
  if (query.isError) return <MetaDiff diff={diff} detail={`The preview failed: ${String((query.error as Error)?.message ?? query.error)}`} />;
  const data = query.data;
  if (!data || data.unsupported || (!data.old && !data.new)) {
    return <MetaDiff diff={diff} detail={data?.unsupported || "There is no preview for this image."} />;
  }
  return <ImageCompareView compare={data} />;
}

function usePixels(src: string | undefined): Pixels | null {
  const [pixels, setPixels] = useState<Pixels | null>(null);
  useEffect(() => {
    setPixels(null);
    if (!src) return;
    let live = true;
    loadPixels(src).then(
      (p) => live && setPixels(p),
      () => live && setPixels(null),
    );
    return () => {
      live = false;
    };
  }, [src]);
  return pixels;
}

function useBoxSize(ref: RefObject<HTMLElement | null>) {
  const [size, setSize] = useState({ width: 0, height: 0 });
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => setSize({ width: el.clientWidth, height: el.clientHeight });
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [ref]);
  return size;
}

function readMode(): CompareMode {
  try {
    const stored = localStorage.getItem(modeKey);
    return modes.find((m) => m.id === stored)?.id ?? "side";
  } catch {
    return "side";
  }
}

export function ImageCompareView({ compare }: { compare: ImageCompare }) {
  const { old, new: next, stats } = compare;
  const both = !!old && !!next;
  const [storedMode, setStoredMode] = useState<CompareMode>(readMode);
  const mode = both ? storedMode : null;
  const [channels, setChannels] = useState<Channels>(allChannels);
  const [zoom, setZoom] = useState<number | null>(null);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [swipe, setSwipe] = useState(50);
  const [opacity, setOpacity] = useState(50);
  const [tolerance, setTolerance] = useState(0);
  const [blinkNew, setBlinkNew] = useState(true);

  const oldPixels = usePixels(old?.src);
  const newPixels = usePixels(next?.src);
  const oldView = useMemo(() => oldPixels && isolateChannels(oldPixels, channels), [oldPixels, channels]);
  const newView = useMemo(() => newPixels && isolateChannels(newPixels, channels), [newPixels, channels]);
  const heat = useMemo(
    () => (mode === "difference" && oldView && newView ? heatmap(oldView, newView, tolerance) : null),
    [mode, oldView, newView, tolerance],
  );

  useEffect(() => {
    if (mode !== "blink") return;
    const id = setInterval(() => setBlinkNew((v) => !v), blinkMs);
    return () => clearInterval(id);
  }, [mode]);

  const stageRef = useRef<HTMLDivElement>(null);
  const box = useBoxSize(stageRef);
  const width = Math.max(old?.width ?? 0, next?.width ?? 0);
  const height = Math.max(old?.height ?? 0, next?.height ?? 0);
  const paneBox = mode === "side" ? { width: box.width / 2, height: box.height } : box;
  const scale = zoom ?? fitZoom(paneBox, { width, height });

  const chooseMode = (m: CompareMode) => {
    setStoredMode(m);
    try {
      localStorage.setItem(modeKey, m);
    } catch {
      return;
    }
  };
  const zoomTo = (z: number) => {
    const target = clampZoom(z);
    setOffset((o) => ({ x: (o.x * target) / scale, y: (o.y * target) / scale }));
    setZoom(target);
  };
  const fit = () => {
    setZoom(null);
    setOffset({ x: 0, y: 0 });
  };

  const drag = useRef<{ x: number; y: number } | null>(null);
  const pan = {
    onPointerDown: (e: PointerEvent<HTMLDivElement>) => {
      drag.current = { x: e.clientX - offset.x, y: e.clientY - offset.y };
      e.currentTarget.setPointerCapture?.(e.pointerId);
    },
    onPointerMove: (e: PointerEvent<HTMLDivElement>) => {
      if (drag.current) setOffset({ x: e.clientX - drag.current.x, y: e.clientY - drag.current.y });
    },
    onPointerUp: () => {
      drag.current = null;
    },
    onWheel: (e: WheelEvent<HTMLDivElement>) => zoomTo(scale * (e.deltaY < 0 ? 1.15 : 1 / 1.15)),
  };

  const frame: CSSProperties = {
    width: width * scale,
    height: height * scale,
    transform: `translate(calc(-50% + ${offset.x}px), calc(-50% + ${offset.y}px))`,
  };
  const layer = (info: ImageInfo, pixels: Pixels | null, label: string, style?: CSSProperties) => (
    <Layer info={info} pixels={pixels} scale={scale} label={label} style={style} />
  );

  let stage: ReactNode;
  if (mode === "side") {
    stage = (
      <div className="grid h-full grid-cols-2 divide-x divide-border">
        <Pane label="Before" frame={frame} pan={pan}>
          {layer(old!, oldView, "Before")}
        </Pane>
        <Pane label="After" frame={frame} pan={pan}>
          {layer(next!, newView, "After")}
        </Pane>
      </div>
    );
  } else if (mode === "swipe") {
    stage = (
      <Pane frame={frame} pan={pan}>
        {layer(old!, oldView, "Before")}
        {layer(next!, newView, "After", { clipPath: `inset(0 0 0 ${swipe}%)` })}
        <div className="pointer-events-none absolute inset-y-0 w-px bg-accent" style={{ left: `${swipe}%` }} />
      </Pane>
    );
  } else if (mode === "onion") {
    stage = (
      <Pane frame={frame} pan={pan}>
        {layer(old!, oldView, "Before")}
        {layer(next!, newView, "After", { opacity: opacity / 100 })}
      </Pane>
    );
  } else if (mode === "difference") {
    stage = (
      <Pane frame={frame} pan={pan}>
        <PixelCanvas pixels={heat?.pixels ?? null} label="Difference" className="absolute inset-0 size-full" pixelated={scale * width > 1.5 * (heat?.pixels.width ?? width)} />
        {stats && !stats.sizeChanged && stats.boundsWidth > 0 && <Bounds stats={stats} width={width} height={height} />}
      </Pane>
    );
  } else if (mode === "blink") {
    stage = (
      <Pane label={blinkNew ? "After" : "Before"} frame={frame} pan={pan}>
        {blinkNew ? layer(next!, newView, "After") : layer(old!, oldView, "Before")}
      </Pane>
    );
  } else {
    const info = (next ?? old)!;
    stage = (
      <Pane label={next ? "Added" : "Deleted"} frame={frame} pan={pan}>
        {layer(info, next ? newView : oldView, next ? "After" : "Before")}
      </Pane>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-10 shrink-0 items-center gap-2 overflow-x-auto border-b border-border px-3 text-xs">
        {both && (
          <div className="flex rounded-md border border-border p-0.5" role="group" aria-label="Compare mode">
            {modes.map(({ id, label, Icon }) => (
              <ToolButton key={id} active={mode === id} label={label} onClick={() => chooseMode(id)}>
                <Icon className="size-3.5" />
              </ToolButton>
            ))}
          </div>
        )}
        <div className="flex rounded-md border border-border p-0.5" role="group" aria-label="Channels">
          {(["r", "g", "b", "a"] as const).map((c) => (
            <ToolButton key={c} active={channels[c]} label={`${c.toUpperCase()} channel`} onClick={() => setChannels((ch) => ({ ...ch, [c]: !ch[c] }))}>
              <span className="font-mono text-[11px] font-semibold">{c.toUpperCase()}</span>
            </ToolButton>
          ))}
        </div>
        {mode === "swipe" && <Slider label="Swipe position" value={swipe} onChange={setSwipe} />}
        {mode === "onion" && <Slider label="After opacity" value={opacity} onChange={setOpacity} />}
        {mode === "difference" && <Slider label="Tolerance" value={tolerance} max={255} onChange={setTolerance} />}
        {mode === "difference" && heat && heat.total > 0 && (
          <span className="whitespace-nowrap text-muted-foreground">{formatPercent(heat.changed / heat.total)} above tolerance</span>
        )}
        <div className="ml-auto flex items-center gap-0.5">
          {stats &&
            (stats.sizeChanged ? (
              <span className="mr-2 whitespace-nowrap rounded-full bg-muted px-2 py-0.5 font-medium">Resized</span>
            ) : (
              <span className="mr-2 whitespace-nowrap rounded-full bg-muted px-2 py-0.5 font-medium" title={`${stats.changedPixels.toLocaleString()} of ${stats.totalPixels.toLocaleString()} pixels`}>
                {formatPercent(stats.changedRatio)} changed
              </span>
            ))}
          <ToolButton label="Zoom out" onClick={() => zoomTo(stepZoom(scale, -1))}>
            <ZoomOut className="size-3.5" />
          </ToolButton>
          <span className="w-12 text-center font-mono tabular-nums" aria-label="Zoom level">
            {Math.round(scale * 100)}%
          </span>
          <ToolButton label="Zoom in" onClick={() => zoomTo(stepZoom(scale, 1))}>
            <ZoomIn className="size-3.5" />
          </ToolButton>
          <ToolButton label="Actual size" onClick={() => zoomTo(1)}>
            <span className="font-mono text-[11px]">1:1</span>
          </ToolButton>
          <ToolButton label="Fit" active={zoom === null} onClick={fit}>
            <Maximize className="size-3.5" />
          </ToolButton>
        </div>
      </div>
      <div ref={stageRef} className="min-h-0 flex-1">
        {stage}
      </div>
      <MetaTable old={old} next={next} stats={stats} />
    </div>
  );
}

type PanHandlers = {
  onPointerDown: (e: PointerEvent<HTMLDivElement>) => void;
  onPointerMove: (e: PointerEvent<HTMLDivElement>) => void;
  onPointerUp: () => void;
  onWheel: (e: WheelEvent<HTMLDivElement>) => void;
};

function Pane({ label, frame, pan, children }: { label?: string; frame: CSSProperties; pan: PanHandlers; children: ReactNode }) {
  return (
    <div className="checkerboard relative h-full cursor-grab touch-none overflow-hidden active:cursor-grabbing" data-testid="image-pane" {...pan}>
      <div className="absolute left-1/2 top-1/2" style={frame} data-testid="image-frame">
        {children}
      </div>
      {label && <span className="pointer-events-none absolute left-2 top-2 rounded bg-background/80 px-1.5 py-0.5 text-[11px] font-medium shadow-sm">{label}</span>}
    </div>
  );
}

function Layer({ info, pixels, scale, label, style }: { info: ImageInfo; pixels: Pixels | null; scale: number; label: string; style?: CSSProperties }) {
  const box: CSSProperties = { width: info.width * scale, height: info.height * scale, ...style };
  const pixelated = info.previewWidth > 0 && (info.width * scale) / info.previewWidth > 1.5;
  return (
    <div className="absolute left-0 top-0" style={box}>
      <PixelCanvas pixels={pixels} label={label} className="size-full" pixelated={pixelated} />
    </div>
  );
}

function PixelCanvas({ pixels, label, className, pixelated }: { pixels: Pixels | null; label: string; className?: string; pixelated: boolean }) {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    if (ref.current && pixels) drawPixels(ref.current, pixels);
  }, [pixels]);
  return <canvas ref={ref} role="img" aria-label={label} className={className} style={{ imageRendering: pixelated ? "pixelated" : "auto" }} />;
}

function Bounds({ stats, width, height }: { stats: ImageStats; width: number; height: number }) {
  const pct = (v: number, of: number) => `${(v / of) * 100}%`;
  return (
    <div
      aria-label="Changed region"
      className="pointer-events-none absolute border border-dashed border-accent"
      style={{ left: pct(stats.boundsX, width), top: pct(stats.boundsY, height), width: pct(stats.boundsWidth, width), height: pct(stats.boundsHeight, height) }}
    />
  );
}

function ToolButton({ active, label, onClick, children }: { active?: boolean; label: string; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-pressed={active}
      aria-label={label}
      title={label}
      onClick={onClick}
      className={cn("flex h-6 min-w-6 items-center justify-center rounded px-1 text-muted-foreground hover:text-foreground", active && "bg-muted text-foreground")}
    >
      {children}
    </button>
  );
}

function Slider({ label, value, max = 100, onChange }: { label: string; value: number; max?: number; onChange: (v: number) => void }) {
  return (
    <label className="flex items-center gap-2 whitespace-nowrap text-muted-foreground">
      {label}
      <input type="range" min={0} max={max} value={value} onChange={(e) => onChange(Number(e.target.value))} className="w-28 accent-[var(--accent)]" />
    </label>
  );
}

type Row = { label: string; value: (i: ImageInfo) => string };

const rows: Row[] = [
  { label: "Dimensions", value: (i) => `${i.width} × ${i.height}` },
  { label: "Format", value: (i) => i.format.toUpperCase() },
  { label: "Bit depth", value: (i) => (i.bitDepth ? `${i.bitDepth}-bit` : "—") },
  { label: "Alpha", value: (i) => (i.hasAlpha ? "Yes" : "No") },
  { label: "File size", value: (i) => formatBytes(i.size) },
];

function MetaTable({ old, next, stats }: { old: ImageInfo | null; next: ImageInfo | null; stats: ImageStats | null }) {
  return (
    <div className="flex shrink-0 flex-wrap items-start gap-x-8 gap-y-2 border-t border-border px-4 py-2 text-[11px]">
      <table aria-label="Image details">
        <thead>
          <tr className="text-muted-foreground">
            <th />
            <th className="px-3 text-left font-medium">Before</th>
            <th className="px-3 text-left font-medium">After</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const a = old ? r.value(old) : "—";
            const b = next ? r.value(next) : "—";
            return (
              <tr key={r.label}>
                <th className="pr-3 text-left font-normal text-muted-foreground">{r.label}</th>
                <td className="px-3 font-mono">{a}</td>
                <td className={cn("px-3 font-mono", old && next && a !== b && "font-semibold text-warning")}>{b}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {stats && stats.sizeChanged && <p className="text-muted-foreground">Dimensions changed, so pixels are not compared.</p>}
      {stats && !stats.sizeChanged && (
        <dl className="grid grid-cols-[auto_auto] gap-x-3 gap-y-0.5">
          <dt className="text-muted-foreground">Changed pixels</dt>
          <dd className="font-mono">
            {stats.changedPixels.toLocaleString()} ({formatPercent(stats.changedRatio)})
          </dd>
          <dt className="text-muted-foreground">Largest change</dt>
          <dd className="font-mono">{stats.maxDelta} / 255</dd>
          <dt className="text-muted-foreground">Changed region</dt>
          <dd className="font-mono">
            {stats.boundsWidth > 0 ? `${stats.boundsWidth} × ${stats.boundsHeight} at ${stats.boundsX}, ${stats.boundsY}` : "none"}
          </dd>
        </dl>
      )}
    </div>
  );
}
