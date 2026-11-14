import type { VideoStats } from "@/api/desktop";
import { translate , locale } from "@/i18n";

export const DEFAULT_FPS = 30;

export const frameStep = (stats: VideoStats | null | undefined): number => 1 / (stats && stats.frameRate > 0 ? stats.frameRate : DEFAULT_FPS);

export function formatBitrate(bps: number): string {
  if (!bps || bps <= 0) return "—";
  if (bps >= 1_000_000) return translate("video.mbS", { value: (bps / 1_000_000).toFixed(1) });
  if (bps >= 1_000) return translate("video.kbS", { value: Math.round(bps / 1_000) });
  return translate("video.bS", { bps });
}

export const formatResolution = (s: VideoStats): string => (s.width > 0 && s.height > 0 ? translate("video.text", { width: s.width, height: s.height }) : "—");

export function formatFrameRate(fps: number): string {
  if (!fps || fps <= 0) return "—";
  const rounded = Math.round(fps * 100) / 100;
  return translate("video.fps", { value: Number.isInteger(rounded) ? rounded : rounded.toFixed(2) });
}

export function formatAudio(s: VideoStats): string {
  if (!s.audioCodec) return translate("video.none");
  const parts = [s.audioCodec];
  if (s.sampleRate > 0) parts.push(translate("video.kHz", { toLocaleString: (s.sampleRate / 1000).toLocaleString(locale()) }));
  if (s.channels > 0) parts.push(s.channels === 1 ? translate("channels.mono") : s.channels === 2 ? translate("channels.stereo") : translate("video.ch", { channels: s.channels }));
  return parts.join(" · ");
}

// clampTime keeps a seek target inside [0, duration].
export const clampTime = (t: number, duration: number): number => Math.max(0, Math.min(Number.isFinite(duration) && duration > 0 ? duration : t, t));
