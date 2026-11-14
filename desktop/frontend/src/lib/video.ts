import type { VideoStats } from "@/api/desktop";

export const DEFAULT_FPS = 30;

export const frameStep = (stats: VideoStats | null | undefined): number => 1 / (stats && stats.frameRate > 0 ? stats.frameRate : DEFAULT_FPS);

export function formatBitrate(bps: number): string {
  if (!bps || bps <= 0) return "—";
  if (bps >= 1_000_000) return `${(bps / 1_000_000).toFixed(1)} Mb/s`;
  if (bps >= 1_000) return `${Math.round(bps / 1_000)} kb/s`;
  return `${bps} b/s`;
}

export const formatResolution = (s: VideoStats): string => (s.width > 0 && s.height > 0 ? `${s.width}×${s.height}` : "—");

export function formatFrameRate(fps: number): string {
  if (!fps || fps <= 0) return "—";
  const rounded = Math.round(fps * 100) / 100;
  return `${Number.isInteger(rounded) ? rounded : rounded.toFixed(2)} fps`;
}

export function formatAudio(s: VideoStats): string {
  if (!s.audioCodec) return "None";
  const parts = [s.audioCodec];
  if (s.sampleRate > 0) parts.push(`${(s.sampleRate / 1000).toLocaleString()} kHz`);
  if (s.channels > 0) parts.push(s.channels === 1 ? "mono" : s.channels === 2 ? "stereo" : `${s.channels} ch`);
  return parts.join(" · ");
}

// clampTime keeps a seek target inside [0, duration].
export const clampTime = (t: number, duration: number): number => Math.max(0, Math.min(Number.isFinite(duration) && duration > 0 ? duration : t, t));
