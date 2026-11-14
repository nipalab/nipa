import { useRef, useState, type RefObject } from "react";
import { useQuery } from "@tanstack/react-query";
import { ExternalLink, Loader2, Pause, Play, SkipBack, SkipForward } from "lucide-react";
import { desktop, type FileDiff, type VideoCompare, type VideoSide, type VideoStats } from "@/api/desktop";
import { imageRefs, viewerKey } from "@/components/changes/ImageDiff";
import { MetaDiff } from "@/components/changes/Notices";
import { Group, Notice, ToolButton } from "@/components/changes/Toolbar";
import { formatTime } from "@/lib/audio";
import { formatBytes } from "@/lib/diff";
import { clampTime, formatAudio, formatBitrate, formatFrameRate, formatResolution, frameStep } from "@/lib/video";
import { cn } from "@/lib/utils";

type Side = "old" | "new";

export function VideoDiff({ root, diff }: { root: string; diff: FileDiff }) {
  const { old, next } = imageRefs(diff);
  const query = useQuery({
    queryKey: viewerKey(root, diff, "video"),
    queryFn: () => desktop.videoDiff(root, old, next),
    staleTime: 30_000,
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
  if (query.isPending) {
    return (
      <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> Preparing video…
      </div>
    );
  }
  if (query.isError) return <MetaDiff diff={diff} detail={`The video could not be read: ${String((query.error as Error)?.message ?? query.error)}`} />;
  if (!query.data || (!query.data.old && !query.data.new)) return <MetaDiff diff={diff} detail="There is no preview for this video." />;
  return <VideoCompareView compare={query.data} />;
}

const safePlay = async (v: HTMLVideoElement) => {
  try {
    await v.play();
  } catch {
    return;
  }
};

export function VideoCompareView({ compare, onOpen = desktop.openMedia }: { compare: VideoCompare; onOpen?: (token: string) => Promise<void> }) {
  const { old, new: next } = compare;
  const refs: Record<Side, RefObject<HTMLVideoElement>> = { old: useRef<HTMLVideoElement>(null), new: useRef<HTMLVideoElement>(null) };
  const [failed, setFailed] = useState<Record<Side, boolean>>({ old: false, new: false });
  const [playing, setPlaying] = useState(false);
  const [position, setPosition] = useState(0);
  const [listen, setListen] = useState<Side>(next ? "new" : "old");
  const [openError, setOpenError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState<Record<Side, number>>({ old: 0, new: 0 });

  const usable = (side: Side) => {
    const s = side === "old" ? old : next;
    return !!s?.url && !failed[side];
  };
  const master: Side = usable("new") ? "new" : "old";
  const span = Math.max(old?.stats?.duration ?? 0, next?.stats?.duration ?? 0, loaded.old, loaded.new);
  const elements = () => (["old", "new"] as const).filter(usable).map((s) => refs[s].current).filter((v): v is HTMLVideoElement => !!v);
  const canPlay = usable("old") || usable("new");

  const seek = (to: number) => {
    const t = clampTime(to, span);
    for (const v of elements()) v.currentTime = Number.isFinite(v.duration) && v.duration > 0 ? Math.min(t, v.duration) : t;
    setPosition(t);
  };
  const toggle = () => {
    if (!canPlay) return;
    if (playing) {
      for (const v of elements()) v.pause();
      setPlaying(false);
      return;
    }
    const from = position >= span && span > 0 ? 0 : position;
    seek(from);
    for (const v of elements()) void safePlay(v);
    setPlaying(true);
  };
  const step = (dir: 1 | -1) => {
    for (const v of elements()) v.pause();
    setPlaying(false);
    seek(position + dir * frameStep((master === "new" ? next : old)?.stats));
  };
  const follow = (side: Side) => {
    if (side !== master) return;
    const v = refs[side].current;
    if (!v) return;
    setPosition(v.currentTime);
    const other = refs[side === "new" ? "old" : "new"].current;
    if (other && usable(side === "new" ? "old" : "new") && !other.paused && Math.abs(other.currentTime - v.currentTime) > 0.15 && v.currentTime < (other.duration || Infinity)) {
      other.currentTime = v.currentTime;
    }
  };
  const ended = (side: Side) => {
    if (side !== master) return;
    for (const v of elements()) v.pause();
    setPlaying(false);
  };
  const open = (token: string) => {
    setOpenError(null);
    onOpen(token).catch((err: unknown) => setOpenError(err instanceof Error ? err.message : String(err)));
  };

  const notices: string[] = [];
  for (const [label, s] of [["Before", old], ["After", next]] as const) {
    if (s?.statsError) notices.push(`${label}: ${s.statsError}`);
  }
  if (openError) notices.push(openError);
  const both = !!old && !!next;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-3 py-1.5 text-[11px]">
        <ToolButton label={playing ? "Pause" : "Play"} onClick={toggle}>
          {playing ? <Pause className="size-3.5" /> : <Play className="size-3.5" />}
        </ToolButton>
        <ToolButton label="Previous frame" onClick={() => step(-1)}>
          <SkipBack className="size-3.5" />
        </ToolButton>
        <ToolButton label="Next frame" onClick={() => step(1)}>
          <SkipForward className="size-3.5" />
        </ToolButton>
        <input
          type="range"
          aria-label="Seek"
          min={0}
          max={span || 0}
          step={0.001}
          value={Math.min(position, span || 0)}
          disabled={!canPlay}
          onChange={(e) => seek(Number(e.target.value))}
          className="w-48 accent-accent"
        />
        <span className="font-mono tabular-nums text-muted-foreground" aria-label="Playback position">
          {formatTime(position)} / {formatTime(span)}
        </span>
        {both && usable("old") && usable("new") && (
          <Group label="Listen to">
            <ToolButton active={listen === "old"} label="Listen to before" onClick={() => setListen("old")}>
              <span className="px-1">A · Before</span>
            </ToolButton>
            <ToolButton active={listen === "new"} label="Listen to after" onClick={() => setListen("new")}>
              <span className="px-1">B · After</span>
            </ToolButton>
          </Group>
        )}
      </div>
      <div className={cn("grid min-h-0 flex-1 gap-2 bg-card p-2", both ? "grid-cols-2" : "grid-cols-1")}>
        {(["old", "new"] as const).map((which) => {
          const side = which === "old" ? old : next;
          if (!side) return null;
          return (
            <Lane
              key={which}
              label={which === "old" ? (next ? "Before" : "Deleted") : old ? "After" : "Added"}
              side={side}
              videoRef={refs[which]}
              failed={failed[which]}
              muted={both ? listen !== which : false}
              onFail={() => setFailed((f) => ({ ...f, [which]: true }))}
              onMetadata={(d) => setLoaded((l) => ({ ...l, [which]: Number.isFinite(d) ? d : 0 }))}
              onTime={() => follow(which)}
              onEnded={() => ended(which)}
              onOpen={open}
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

type LaneProps = {
  label: string;
  side: VideoSide;
  videoRef: RefObject<HTMLVideoElement>;
  failed: boolean;
  muted: boolean;
  onFail: () => void;
  onMetadata: (duration: number) => void;
  onTime: () => void;
  onEnded: () => void;
  onOpen: (token: string) => void;
};

function Lane({ label, side, videoRef, failed, muted, onFail, onMetadata, onTime, onEnded, onOpen }: LaneProps) {
  const s = side.stats;
  return (
    <section aria-label={label} className="flex min-h-0 flex-col overflow-hidden rounded-md border border-border bg-background">
      <header className="flex items-center gap-2 border-b border-border px-2 py-1 text-[11px]">
        <span className="font-medium">{label}</span>
        <span className="min-w-0 flex-1 truncate text-muted-foreground">{s ? [s.videoCodec, formatResolution(s), formatFrameRate(s.frameRate)].filter((p) => p && p !== "—").join(" · ") : side.path}</span>
        {side.media && (
          <button type="button" onClick={() => onOpen(side.media)} className="flex items-center gap-1 text-muted-foreground hover:text-foreground" title="Open in the default video player">
            <ExternalLink className="size-3" /> Open externally
          </button>
        )}
      </header>
      <div className="flex min-h-0 flex-1 items-center justify-center bg-black/90">
        {!side.url ? (
          <p className="p-4 text-center text-[11px] text-white/70">{side.statsError || "This video can't be previewed."}</p>
        ) : failed ? (
          <div className="flex flex-col items-center gap-2 p-4 text-center text-[11px] text-white/70">
            <p>This video's format can't play in the app{s?.videoCodec ? ` (${s.videoCodec})` : ""}.</p>
            <button type="button" onClick={() => onOpen(side.media)} className="rounded border border-white/30 px-2 py-1 text-white hover:bg-white/10">
              Open externally
            </button>
          </div>
        ) : (
          <video
            ref={videoRef}
            src={side.url}
            preload="metadata"
            playsInline
            muted={muted}
            aria-label={`${label} video`}
            className="max-h-full max-w-full"
            onError={onFail}
            onLoadedMetadata={(e) => onMetadata(e.currentTarget.duration)}
            onTimeUpdate={onTime}
            onSeeked={onTime}
            onEnded={onEnded}
          />
        )}
      </div>
    </section>
  );
}

type StatRow = { label: string; text: (s: VideoStats) => string };

const statRows: StatRow[] = [
  { label: "Container", text: (s) => s.container.toUpperCase() },
  { label: "Duration", text: (s) => formatTime(s.duration) },
  { label: "Resolution", text: formatResolution },
  { label: "Video", text: (s) => s.videoCodec || "None" },
  { label: "Frame rate", text: (s) => formatFrameRate(s.frameRate) },
  { label: "Audio", text: formatAudio },
  { label: "Bitrate", text: (s) => formatBitrate(s.bitrate) },
];

function StatsTable({ old, next }: { old: VideoSide | null; next: VideoSide | null }) {
  const [a, b] = [old?.stats ?? null, next?.stats ?? null];
  return (
    <table aria-label="Video details">
      <thead>
        <tr className="text-muted-foreground">
          <th />
          <th className="px-3 text-left font-medium">Before</th>
          <th className="px-3 text-left font-medium">After</th>
        </tr>
      </thead>
      <tbody className="font-mono">
        <tr>
          <th className="pr-3 text-left font-sans font-normal text-muted-foreground">File size</th>
          <td className="px-3">{old ? formatBytes(old.size) : "—"}</td>
          <td className="px-3">{next ? formatBytes(next.size) : "—"}</td>
        </tr>
        {statRows.map((r) => {
          const changed = !!a && !!b && r.text(a) !== r.text(b);
          return (
            <tr key={r.label}>
              <th className="pr-3 text-left font-sans font-normal text-muted-foreground">{r.label}</th>
              <td className="px-3">{a ? r.text(a) : "—"}</td>
              <td className={cn("px-3", changed && "font-semibold text-warning")}>{b ? r.text(b) : "—"}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
