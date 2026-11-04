export type Rgb = [number, number, number];

const stops: Rgb[] = [
  [0, 0, 4],
  [40, 11, 84],
  [101, 21, 110],
  [159, 42, 99],
  [212, 72, 66],
  [245, 125, 21],
  [250, 193, 39],
  [252, 255, 164],
];

export function colormap(v: number): Rgb {
  const t = (Math.max(0, Math.min(255, v)) / 255) * (stops.length - 1);
  const i = Math.min(stops.length - 2, Math.floor(t));
  const f = t - i;
  const [a, b] = [stops[i], stops[i + 1]];
  return [Math.round(a[0] + (b[0] - a[0]) * f), Math.round(a[1] + (b[1] - a[1]) * f), Math.round(a[2] + (b[2] - a[2]) * f)];
}

// spectrogramPixels turns rows×cols levels (row 0 = lowest frequency) into
// RGBA pixels with the highest frequency on top.
export function spectrogramPixels(levels: Uint8Array, cols: number, rows: number): Uint8ClampedArray {
  const out = new Uint8ClampedArray(cols * rows * 4);
  const lut = Array.from({ length: 256 }, (_, v) => colormap(v));
  for (let row = 0; row < rows; row++) {
    const y = rows - 1 - row;
    for (let col = 0; col < cols; col++) {
      const [r, g, b] = lut[levels[row * cols + col] ?? 0];
      const o = (y * cols + col) * 4;
      out[o] = r;
      out[o + 1] = g;
      out[o + 2] = b;
      out[o + 3] = 255;
    }
  }
  return out;
}

export type WaveformTarget = Pick<CanvasRenderingContext2D, "clearRect" | "fillRect"> & { fillStyle: string | CanvasGradient | CanvasPattern };

// drawWaveform draws min/max pairs (one per bucket) into a width×height box,
// one column per pixel, with full scale touching the box edges.
export function drawWaveform(ctx: WaveformTarget, peaks: number[], width: number, height: number, color: string, top = 0) {
  const buckets = Math.floor(peaks.length / 2);
  ctx.fillStyle = color;
  ctx.fillRect(0, top + height / 2, width, 1);
  if (buckets === 0) return;
  const y = (v: number) => top + ((1 - Math.max(-1, Math.min(1, v))) / 2) * height;
  for (let x = 0; x < width; x++) {
    const from = Math.floor((x * buckets) / width);
    const to = Math.max(from + 1, Math.floor(((x + 1) * buckets) / width));
    let lo = Infinity;
    let hi = -Infinity;
    for (let b = from; b < to && b < buckets; b++) {
      lo = Math.min(lo, peaks[2 * b]);
      hi = Math.max(hi, peaks[2 * b + 1]);
    }
    if (lo === Infinity) continue;
    ctx.fillRect(x, y(hi), 1, Math.max(1, y(lo) - y(hi)));
  }
}

export function formatTime(seconds: number): string {
  const s = Math.max(0, seconds);
  const m = Math.floor(s / 60);
  const rest = s - m * 60;
  return `${m}:${rest.toFixed(2).padStart(5, "0")}`;
}

export const formatDb = (v: number | null | undefined, unit = "dBFS") => (v === null || v === undefined ? "−∞" : `${v.toFixed(1)} ${unit}`);

export type Side = "old" | "new";

type SourceLike = Pick<AudioBufferSourceNode, "connect" | "start" | "stop"> & { buffer: AudioBuffer | null; onended: (() => void) | null };
type GainLike = Pick<GainNode, "connect"> & { gain: { value: number } };

export type AudioContextLike = {
  currentTime: number;
  destination: AudioNode;
  decodeAudioData(data: ArrayBuffer): Promise<AudioBuffer>;
  createBufferSource(): SourceLike;
  createGain(): GainLike;
  resume(): Promise<void>;
  close(): Promise<void>;
};

export type Player = {
  load(old: Uint8Array | null, next: Uint8Array | null): Promise<void>;
  play(from: number): void;
  pause(): void;
  seek(to: number): void;
  setSide(side: Side): void;
  position(): number;
  playing(): boolean;
  duration(): number;
  dispose(): void;
};

// createPlayer plays both versions in lockstep and lets the listener flip
// between them: both sources run, only the chosen side is audible.
export function createPlayer(makeContext: () => AudioContextLike, onEnded: () => void = () => {}): Player {
  let ctx: AudioContextLike | null = null;
  const buffers: Record<Side, AudioBuffer | null> = { old: null, new: null };
  let side: Side = "new";
  let running: { source: SourceLike; gain: GainLike; side: Side }[] = [];
  let startedAt = 0;
  let pausedAt = 0;
  let isPlaying = false;

  const duration = () => Math.max(buffers.old?.duration ?? 0, buffers.new?.duration ?? 0);
  const stopAll = () => {
    for (const r of running) {
      r.source.onended = null;
      r.source.stop();
    }
    running = [];
  };
  const position = () => (isPlaying && ctx ? Math.min(duration(), ctx.currentTime - startedAt) : pausedAt);

  const play = (from: number) => {
    if (!ctx) return;
    stopAll();
    void ctx.resume();
    const at = Math.max(0, Math.min(from, duration()));
    const longest: Side = (buffers.new?.duration ?? 0) >= (buffers.old?.duration ?? 0) ? "new" : "old";
    for (const s of ["old", "new"] as Side[]) {
      const buffer = buffers[s];
      if (!buffer || at >= buffer.duration) continue;
      const source = ctx.createBufferSource();
      source.buffer = buffer;
      const gain = ctx.createGain();
      gain.gain.value = s === side ? 1 : 0;
      source.connect(gain as unknown as AudioNode);
      gain.connect(ctx.destination);
      if (s === longest) {
        source.onended = () => {
          isPlaying = false;
          pausedAt = 0;
          running = [];
          onEnded();
        };
      }
      source.start(0, at);
      running.push({ source, gain, side: s });
    }
    startedAt = ctx.currentTime - at;
    isPlaying = running.length > 0;
    pausedAt = isPlaying ? 0 : at;
  };

  return {
    async load(old, next) {
      if (!ctx) ctx = makeContext();
      const decode = (bytes: Uint8Array | null) =>
        bytes && bytes.length > 0 ? ctx!.decodeAudioData(bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer) : Promise.resolve(null);
      [buffers.old, buffers.new] = await Promise.all([decode(old), decode(next)]);
      if (!buffers.new) side = "old";
    },
    play,
    pause() {
      if (!isPlaying) return;
      pausedAt = position();
      isPlaying = false;
      stopAll();
    },
    seek(to) {
      if (isPlaying) play(to);
      else pausedAt = Math.max(0, Math.min(to, duration()));
    },
    setSide(next) {
      side = next;
      for (const r of running) r.gain.gain.value = r.side === next ? 1 : 0;
    },
    position,
    playing: () => isPlaying,
    duration,
    dispose() {
      stopAll();
      isPlaying = false;
      void ctx?.close();
      ctx = null;
    },
  };
}
