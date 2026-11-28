import { useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { AudioLines, Loader2, Pause, Play, Square } from "lucide-react";
import { desktop, type AudioCompare, type AudioSide, type AudioStats, type FileDiff } from "@/api/desktop";
import { imageRefs, viewerKey } from "@/components/changes/ImageDiff";
import { MetaDiff } from "@/components/changes/Notices";
import { Group, Notice, ToolButton } from "@/components/changes/Toolbar";
import { createPlayer, drawWaveform, formatDb, formatTime, spectrogramPixels, type AudioContextLike, type Player, type Side } from "@/lib/audio";
import { formatBytes } from "@/lib/diff";
import { base64ToBytes, cn } from "@/lib/utils";
import { fetchBytes, mediaExpired } from "@/lib/media";
import { useTranslation } from "react-i18next";
import { translate , locale } from "@/i18n";
import { withAbort } from "@/lib/abort";

export function AudioDiff({ root, diff }: { root: string; diff: FileDiff }) {
  const { t } = useTranslation();
  const { old, next } = imageRefs(diff);
  const query = useQuery({
    queryKey: viewerKey(root, diff, "audio"),
    queryFn: ({ signal }) => withAbort(signal, desktop.audioDiff(root, old, next)),
    staleTime: 30_000,
    // Playback copies are media files released when the viewer closes, so
    // cached results are not kept long.
    gcTime: 30_000,
    refetchOnWindowFocus: false,
  });
  const reloaded = useRef<string | null>(null);
  if (query.isPending) {
    return (
      <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> {t("audioDiff.analysingAudio")}
      </div>
    );
  }
  if (query.isError) return <MetaDiff diff={diff} detail={translate("audioDiff.theAudioCouldNotBe", { value: String((query.error as Error)?.message ?? query.error) })} />;
  if (!query.data || (!query.data.old && !query.data.new)) return <MetaDiff diff={diff} detail={translate("audioDiff.thereIsNoPreviewFor")} />;
  const data = query.data;
  const reload = () => {
    if (reloaded.current === diff.path) return false;
    reloaded.current = diff.path;
    void query.refetch();
    return true;
  };
  return <AudioCompareView key={`${data.old?.media ?? ""}:${data.new?.media ?? ""}`} compare={data} onExpired={reload} />;
}

type View = "wave" | "spectrum" | "both";

const defaultContext = (): AudioContextLike => {
  const Ctor = (globalThis as { AudioContext?: new () => AudioContext }).AudioContext;
  if (!Ctor) throw new Error(translate("audioDiff.thisSystemHasNoAudio"));
  return new Ctor() as unknown as AudioContextLike;
};

const playable = (side: AudioSide | null) => !!side && !!side.playbackUrl;

const playbackBytes = (side: AudioSide | null, signal: AbortSignal) => (side?.playbackUrl ? fetchBytes(side.playbackUrl, signal) : Promise.resolve(null));

type CompareProps = {
  compare: AudioCompare;
  makeContext?: () => AudioContextLike;
  hasMedia?: (token: string) => Promise<boolean>;
  release?: (tokens: string[]) => Promise<void>;
  // onExpired reloads the comparison when a playback file was evicted; it
  // returns false when a reload was already tried.
  onExpired?: () => boolean;
};

export function AudioCompareView({ compare, makeContext = defaultContext, hasMedia = desktop.hasMedia, release = desktop.releaseMedia, onExpired }: CompareProps) {
  const { t } = useTranslation();
  const { old, new: next } = compare;
  const tokens = [old?.media, next?.media].filter((t): t is string => !!t).join(" ");
  useEffect(() => {
    if (!tokens) return;
    return () => {
      release(tokens.split(" ")).catch(() => undefined);
    };
  }, [tokens, release]);
  const expiredRef = useRef(onExpired);
  expiredRef.current = onExpired;
  const sides = useMemo(() => [old, next].filter((s): s is AudioSide => !!s), [old, next]);
  const both = !!old?.stats && !!next?.stats;
  const [view, setView] = useState<View>("both");
  const [listen, setListen] = useState<Side>(next?.playbackUrl ? "new" : "old");
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
    const abort = new AbortController();
    setReady(false);
    setPlaying(false);
    setPosition(0);
    setPlayError(null);
    Promise.all([playbackBytes(old, abort.signal), playbackBytes(next, abort.signal)])
      .then(([a, b]) => p.load(a, b))
      .then(
        () => {
          if (abort.signal.aborted) return;
          p.setSide(listenRef.current);
          setReady(true);
        },
        (err: unknown) => {
          if (abort.signal.aborted) return;
          const message = err instanceof Error ? err.message : String(err);
          void mediaExpired([old?.media, next?.media], hasMedia).then((expired) => {
            if (abort.signal.aborted || (expired && expiredRef.current?.())) return;
            setPlayError(expired ? translate("audioDiff.expired") : message);
          });
        },
      );
    return () => {
      abort.abort();
      p.dispose();
      player.current = null;
    };
  }, [old, next, makeContext, hasMedia]);

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
  for (const [label, s] of [[translate("audioDiff.before"), old], [translate("audioDiff.after"), next]] as const) {
    if (!s) continue;
    if (s.error) notices.push(translate("audioDiff.text", { label, error: s.error }));
    if (s.stats?.truncated) notices.push(translate("audioDiff.onlyTheFirstWere", { label, value: formatTime(s.stats.duration) }));
    if (s.playbackTruncated) notices.push(translate("audioDiff.playbackStopsEarlyThe", { label }));
    if (s.stats?.clipped) notices.push(translate("audioDiff.samplesAtFullScale", { label, toLocaleString: s.stats.clipped.toLocaleString(locale()) }));
  }
  if (playError) notices.push(playError);
  const canPlay = (playable(old) || playable(next)) && !playError;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-3 py-1.5 text-[11px]">
        <ToolButton label={playing ? translate("audioDiff.pause") : translate("audioDiff.play")} onClick={toggle}>
          {playing ? <Pause className="size-3.5" /> : <Play className="size-3.5" />}
        </ToolButton>
        <ToolButton label={t("audioDiff.stop")} onClick={stop}>
          <Square className="size-3" />
        </ToolButton>
        <span className="font-mono tabular-nums text-muted-foreground" aria-label={t("audioDiff.playbackPosition")}>
          {formatTime(position)} / {formatTime(span)}
        </span>
        {playable(old) && playable(next) && (
          <Group label={t("audioDiff.listenTo")}>
            <ToolButton active={listen === "old"} label={t("audioDiff.listenToBefore")} onClick={() => setListen("old")}>
              <span className="px-1">{t("audioDiff.aBefore")}</span>
            </ToolButton>
            <ToolButton active={listen === "new"} label={t("audioDiff.listenToAfter")} onClick={() => setListen("new")}>
              <span className="px-1">{t("audioDiff.bAfter")}</span>
            </ToolButton>
          </Group>
        )}
        <div className="ml-auto">
          <Group label={t("audioDiff.view")}>
            <ToolButton active={view === "wave"} label={t("audioDiff.waveform")} onClick={() => setView("wave")}>
              <AudioLines className="size-3.5" />
            </ToolButton>
            <ToolButton active={view === "spectrum"} label={t("audioDiff.spectrogram")} onClick={() => setView("spectrum")}>
              <span className="px-1">{t("audioDiff.spectrum")}</span>
            </ToolButton>
            <ToolButton active={view === "both"} label={t("audioDiff.waveformAndSpectrogram")} onClick={() => setView("both")}>
              <span className="px-1">{t("audioDiff.both")}</span>
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
              label={which === "old" ? (next ? translate("audioDiff.before") : translate("audioDiff.deleted")) : old ? translate("audioDiff.after") : translate("audioDiff.added")}
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
  const { t } = useTranslation();
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
        <span className="truncate text-muted-foreground">{stats ? translate("audioDiff.kHz", { codec: stats.codec, toLocaleString: (stats.sampleRate / 1000).toLocaleString(locale()), value: channelName(stats.channels) }) : side.path}</span>
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
        <p className="p-3 text-[11px] text-muted-foreground">{t("audioDiff.noWaveformForThisVersion")}</p>
      )}
    </section>
  );
}

const channelName = (n: number) => (n === 1 ? translate("channels.mono") : n === 2 ? translate("channels.stereo") : translate("audioDiff.channels", { n }));

function Waveform({ peaks }: { peaks: (number[] | null)[] }) {
  const { t } = useTranslation();
  const ref = useRef<HTMLCanvasElement>(null);
  const rows = Math.max(1, peaks.length);
  useEffect(() => {
    const ctx = ref.current?.getContext("2d");
    if (!ctx) return;
    const color = getComputedStyle(ref.current!).color || "#6366f1";
    ctx.clearRect(0, 0, WAVE_WIDTH, LANE_HEIGHT * rows);
    peaks.forEach((p, c) => drawWaveform(ctx, p ?? [], WAVE_WIDTH, LANE_HEIGHT, color, c * LANE_HEIGHT));
  }, [peaks, rows]);
  return <canvas ref={ref} width={WAVE_WIDTH} height={LANE_HEIGHT * rows} className="block w-full text-accent" style={{ height: rows > 2 ? 40 * rows : 64 * rows }} aria-label={t("audioDiff.waveform")} />;
}

function Spectrogram({ side }: { side: AudioSide }) {
  const { t } = useTranslation();
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
      <canvas ref={ref} width={Math.max(1, cols)} height={Math.max(1, rows)} className="block h-24 w-full" aria-label={t("audioDiff.spectrogram")} />
      <span className="pointer-events-none absolute right-1 top-0.5 font-mono text-[10px] text-white/70">{Math.round(side.specMaxHz).toLocaleString(locale())} Hz</span>
      <span className="pointer-events-none absolute bottom-0.5 right-1 font-mono text-[10px] text-white/70">{Math.round(side.specMinHz).toLocaleString(locale())} Hz</span>
    </div>
  );
}

type StatRow = { label: string; text: (s: AudioStats) => string; value?: (s: AudioStats) => number | null; unit?: string };

const statRows: StatRow[] = [
  { label: translate("audioDiff.duration"), text: (s) => formatTime(s.duration), value: (s) => s.duration, unit: "s" },
  { label: translate("audioDiff.sampleRate"), text: (s) => translate("audioDiff.hz", { toLocaleString: s.sampleRate.toLocaleString(locale()) }) },
  { label: translate("audioDiff.channels2"), text: (s) => channelName(s.channels) },
  { label: translate("audioDiff.encoding"), text: (s) => s.codec },
  { label: translate("audioDiff.peak"), text: (s) => formatDb(s.peakDb), value: (s) => s.peakDb, unit: "dB" },
  { label: "RMS", text: (s) => formatDb(s.rmsDb), value: (s) => s.rmsDb, unit: "dB" },
  { label: translate("audioDiff.loudness"), text: (s) => formatDb(s.loudness, "LUFS"), value: (s) => s.loudness, unit: "LU" },
];

function Change({ value, unit }: { value: number; unit: string }) {
  if (Math.abs(value) < 0.05) return null;
  return <span className={value > 0 ? "text-warning" : "text-accent"}>{translate("audioDiff.text2", { value: value > 0 ? "+" : "−", value2: Math.abs(value).toFixed(unit === "s" ? 2 : 1), unit })}</span>;
}

function StatsTable({ old, next }: { old: AudioSide | null; next: AudioSide | null }) {
  const { t } = useTranslation();
  const [a, b] = [old?.stats ?? null, next?.stats ?? null];
  const both = !!a && !!b;
  return (
    <table aria-label={t("audioDiff.audioDetails")}>
      <thead>
        <tr className="text-muted-foreground">
          <th />
          <th className="px-3 text-left font-medium">{t("audioDiff.before")}</th>
          <th className="px-3 text-left font-medium">{t("audioDiff.after")}</th>
          {both && <th className="px-3 text-left font-medium">{t("audioDiff.change")}</th>}
        </tr>
      </thead>
      <tbody className="font-mono">
        <tr>
          <th className="pr-3 text-left font-sans font-normal text-muted-foreground">{t("audioDiff.fileSize")}</th>
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
