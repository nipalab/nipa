import { useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { AudioLines, Loader2, Pause, Play, Square } from "lucide-react";
import { desktop, type AudioCompare, type AudioSide, type AudioStats, type FileDiff } from "@/api/desktop";
import { imageRefs } from "@/components/changes/ImageDiff";
import { MetaDiff } from "@/components/changes/Notices";
import { Group, Notice, ToolButton } from "@/components/changes/Toolbar";
import { createPlayer, drawWaveform, formatDb, formatTime, spectrogramPixels, type AudioContextLike, type Player, type Side } from "@/lib/audio";
import { formatBytes } from "@/lib/diff";
import { base64ToBytes, cn } from "@/lib/utils";

export function AudioDiff({ root, diff }: { root: string; diff: FileDiff }) {
  const { old, next } = imageRefs(diff);
  const query = useQuery({
    queryKey: ["diff", root, diff.path, "audio"],
    queryFn: () => desktop.audioDiff(root, old, next),
    staleTime: 30_000,
  });
  if (query.isPending) {
    return (
      <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> Analysing audio…
      </div>
    );
  }
  if (query.isError) return <MetaDiff diff={diff} detail={`The audio could not be read: ${String((query.error as Error)?.message ?? query.error)}`} />;
  if (!query.data || (!query.data.old && !query.data.new)) return <MetaDiff diff={diff} detail="There is no preview for this audio file." />;
  return <AudioCompareView compare={query.data} />;
}

type View = "wave" | "spectrum" | "both";

const defaultContext = (): AudioContextLike => {
  const Ctor = (globalThis as { AudioContext?: new () => AudioContext }).AudioContext;
  if (!Ctor) throw new Error("This system has no audio output for previews.");
  return new Ctor() as unknown as AudioContextLike;
};

const playable = (side: AudioSide | null) => !!side && !!side.playback;

export function AudioCompareView({ compare, makeContext = defaultContext }: { compare: AudioCompare; makeContext?: () => AudioContextLike }) {
  const { old, new: next } = compare;
  const sides = useMemo(() => [old, next].filter((s): s is AudioSide => !!s), [old, next]);
  const both = !!old?.stats && !!next?.stats;
  const [view, setView] = useState<View>("both");
  const [listen, setListen] = useState<Side>(next?.playback ? "new" : "old");
  const [playing, setPlaying] = useState(false);
  const [position, setPosition] = useState(0);
  const [playError, setPlayError] = useState<string | null>(null);
  const [ready, setReady] = useState(false);
  const player = useRef<Player | null>(null);
  const listenRef = useRef(listen);
  listenRef.current = listen;
  const span = Math.max(old?.stats?.duration ?? 0, next?.stats?.duration ?? 0);

  useEffect(() => {
    if (!playable(old) && !playable(next)) return;
    const p = createPlayer(makeContext, () => {
      setPlaying(false);
      setPosition(0);
    });
    player.current = p;
    let live = true;
    setReady(false);
    setPlaying(false);
    setPosition(0);
    setPlayError(null);
    p.load(old?.playback ? base64ToBytes(old.playback) : null, next?.playback ? base64ToBytes(next.playback) : null).then(
      () => {
        if (!live) return;
        p.setSide(listenRef.current);
        setReady(true);
      },
      (err: unknown) => live && setPlayError(err instanceof Error ? err.message : String(err)),
    );
    return () => {
      live = false;
      p.dispose();
      player.current = null;
    };
  }, [old, next, makeContext]);

  useEffect(() => player.current?.setSide(listen), [listen, ready]);

  useEffect(() => {
    if (!playing) return;
    let frame = 0;
    const tick = () => {
      setPosition(player.current?.position() ?? 0);
      frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [playing]);

  const toggle = () => {
    const p = player.current;
    if (!p || !ready) return;
    if (playing) {
      p.pause();
      setPosition(p.position());
      setPlaying(false);
      return;
    }
    p.play(position >= span ? 0 : position);
    setPlaying(p.playing());
  };
  const stop = () => {
    player.current?.pause();
    player.current?.seek(0);
    setPlaying(false);
    setPosition(0);
  };
  const seek = (to: number) => {
    player.current?.seek(to);
    setPosition(to);
  };

  const notices: string[] = [];
  for (const [label, s] of [["Before", old], ["After", next]] as const) {
    if (!s) continue;
    if (s.error) notices.push(`${label}: ${s.error}`);
    if (s.stats?.truncated) notices.push(`${label}: only the first ${formatTime(s.stats.duration)} were analysed.`);
    if (s.playbackTruncated) notices.push(`${label}: playback stops early, the file is too long to load whole.`);
    if (s.stats?.clipped) notices.push(`${label}: ${s.stats.clipped.toLocaleString()} samples at full scale (clipping).`);
  }
  if (playError) notices.push(playError);
  const canPlay = (playable(old) || playable(next)) && !playError;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-3 py-1.5 text-[11px]">
        <ToolButton label={playing ? "Pause" : "Play"} onClick={toggle}>
          {playing ? <Pause className="size-3.5" /> : <Play className="size-3.5" />}
        </ToolButton>
        <ToolButton label="Stop" onClick={stop}>
          <Square className="size-3" />
        </ToolButton>
        <span className="font-mono tabular-nums text-muted-foreground" aria-label="Playback position">
          {formatTime(position)} / {formatTime(span)}
        </span>
        {playable(old) && playable(next) && (
          <Group label="Listen to">
            <ToolButton active={listen === "old"} label="Listen to before" onClick={() => setListen("old")}>
              <span className="px-1">A · Before</span>
            </ToolButton>
            <ToolButton active={listen === "new"} label="Listen to after" onClick={() => setListen("new")}>
              <span className="px-1">B · After</span>
            </ToolButton>
          </Group>
        )}
        <div className="ml-auto">
          <Group label="View">
            <ToolButton active={view === "wave"} label="Waveform" onClick={() => setView("wave")}>
              <AudioLines className="size-3.5" />
            </ToolButton>
            <ToolButton active={view === "spectrum"} label="Spectrogram" onClick={() => setView("spectrum")}>
              <span className="px-1">Spectrum</span>
            </ToolButton>
            <ToolButton active={view === "both"} label="Waveform and spectrogram" onClick={() => setView("both")}>
              <span className="px-1">Both</span>
            </ToolButton>
          </Group>
        </div>
      </div>
      <div className="min-h-0 flex-1 space-y-3 overflow-auto bg-card p-3">
        {sides.map((s) => {
          const which: Side = s === old ? "old" : "new";
          return (
            <Lane
              key={which}
              label={which === "old" ? (next ? "Before" : "Deleted") : old ? "After" : "Added"}
              side={s}
              view={view}
              span={span}
              position={position}
              audible={canPlay && (both ? listen === which : true)}
              onSeek={canPlay ? seek : undefined}
            />
          );
        })}
      </div>
      <div className="flex max-h-48 shrink-0 flex-wrap items-start gap-x-8 gap-y-2 overflow-auto border-t border-border px-4 py-2 text-[11px]">
        <StatsTable old={old} next={next} />
        {notices.length > 0 && (
          <div className="flex flex-col gap-1">
            {notices.map((n) => (
              <Notice key={n}>{n}</Notice>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

const WAVE_WIDTH = 1200;
const LANE_HEIGHT = 88;

function Lane({ label, side, view, span, position, audible, onSeek }: { label: string; side: AudioSide; view: View; span: number; position: number; audible: boolean; onSeek?: (to: number) => void }) {
  const stats = side.stats;
  const width = span > 0 && stats ? (stats.duration / span) * 100 : 100;
  const click = (e: MouseEvent<HTMLDivElement>) => {
    if (!onSeek || !stats) return;
    const box = e.currentTarget.getBoundingClientRect();
    const f = box.width > 0 ? (e.clientX - box.left) / box.width : 0;
    onSeek(Math.max(0, Math.min(1, f)) * stats.duration);
  };
  return (
    <section aria-label={label} className={cn("rounded-md border border-border bg-background", audible && "ring-1 ring-accent")}>
      <header className="flex items-center gap-2 border-b border-border px-2 py-1 text-[11px]">
        <span className="font-medium">{label}</span>
        <span className="truncate text-muted-foreground">{stats ? `${stats.codec} · ${(stats.sampleRate / 1000).toLocaleString()} kHz · ${channelName(stats.channels)}` : side.path}</span>
      </header>
      {stats ? (
        <div className="relative" style={{ width: `${width}%` }} onClick={click} data-testid={`lane-${label}`}>
          {view !== "spectrum" && <Waveform peaks={side.peaks ?? []} />}
          {view !== "wave" && <Spectrogram side={side} />}
          {position > 0 && position <= stats.duration && (
            <div className="pointer-events-none absolute inset-y-0 w-px bg-accent" style={{ left: `${(position / stats.duration) * 100}%` }} data-testid="playhead" />
          )}
        </div>
      ) : (
        <p className="p-3 text-[11px] text-muted-foreground">No waveform for this version.</p>
      )}
    </section>
  );
}

const channelName = (n: number) => (n === 1 ? "mono" : n === 2 ? "stereo" : `${n} channels`);

function Waveform({ peaks }: { peaks: (number[] | null)[] }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const rows = Math.max(1, peaks.length);
  useEffect(() => {
    const ctx = ref.current?.getContext("2d");
    if (!ctx) return;
    const color = getComputedStyle(ref.current!).color || "#6366f1";
    ctx.clearRect(0, 0, WAVE_WIDTH, LANE_HEIGHT * rows);
    peaks.forEach((p, c) => drawWaveform(ctx, p ?? [], WAVE_WIDTH, LANE_HEIGHT, color, c * LANE_HEIGHT));
  }, [peaks, rows]);
  return <canvas ref={ref} width={WAVE_WIDTH} height={LANE_HEIGHT * rows} className="block w-full text-accent" style={{ height: rows > 2 ? 40 * rows : 64 * rows }} aria-label="Waveform" />;
}

function Spectrogram({ side }: { side: AudioSide }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const { specColumns: cols, specRows: rows } = side;
  useEffect(() => {
    const ctx = ref.current?.getContext("2d");
    if (!ctx || cols <= 0 || rows <= 0) return;
    const pixels = spectrogramPixels(base64ToBytes(side.spectrogram), cols, rows);
    ctx.putImageData(new ImageData(pixels as unknown as Uint8ClampedArray<ArrayBuffer>, cols, rows), 0, 0);
  }, [side, cols, rows]);
  return (
    <div className="relative">
      <canvas ref={ref} width={Math.max(1, cols)} height={Math.max(1, rows)} className="block h-24 w-full" aria-label="Spectrogram" />
      <span className="pointer-events-none absolute right-1 top-0.5 font-mono text-[10px] text-white/70">{Math.round(side.specMaxHz).toLocaleString()} Hz</span>
      <span className="pointer-events-none absolute bottom-0.5 right-1 font-mono text-[10px] text-white/70">{Math.round(side.specMinHz).toLocaleString()} Hz</span>
    </div>
  );
}

type StatRow = { label: string; text: (s: AudioStats) => string; value?: (s: AudioStats) => number | null; unit?: string };

const statRows: StatRow[] = [
  { label: "Duration", text: (s) => formatTime(s.duration), value: (s) => s.duration, unit: "s" },
  { label: "Sample rate", text: (s) => `${s.sampleRate.toLocaleString()} Hz` },
  { label: "Channels", text: (s) => channelName(s.channels) },
  { label: "Encoding", text: (s) => s.codec },
  { label: "Peak", text: (s) => formatDb(s.peakDb), value: (s) => s.peakDb, unit: "dB" },
  { label: "RMS", text: (s) => formatDb(s.rmsDb), value: (s) => s.rmsDb, unit: "dB" },
  { label: "Loudness", text: (s) => formatDb(s.loudness, "LUFS"), value: (s) => s.loudness, unit: "LU" },
];

function Change({ value, unit }: { value: number; unit: string }) {
  if (Math.abs(value) < 0.05) return null;
  return <span className={value > 0 ? "text-warning" : "text-accent"}>{`${value > 0 ? "+" : "−"}${Math.abs(value).toFixed(unit === "s" ? 2 : 1)} ${unit}`}</span>;
}

function StatsTable({ old, next }: { old: AudioSide | null; next: AudioSide | null }) {
  const [a, b] = [old?.stats ?? null, next?.stats ?? null];
  const both = !!a && !!b;
  return (
    <table aria-label="Audio details">
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
        {statRows.map((r) => {
          const va = a && r.value ? r.value(a) : null;
          const vb = b && r.value ? r.value(b) : null;
          const changed = both && r.text(a!) !== r.text(b!);
          return (
            <tr key={r.label}>
              <th className="pr-3 text-left font-sans font-normal text-muted-foreground">{r.label}</th>
              <td className="px-3">{a ? r.text(a) : "—"}</td>
              <td className={cn("px-3", changed && !r.value && "font-semibold text-warning")}>{b ? r.text(b) : "—"}</td>
              {both && <td className="px-3">{va !== null && vb !== null && <Change value={vb - va} unit={r.unit!} />}</td>}
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
