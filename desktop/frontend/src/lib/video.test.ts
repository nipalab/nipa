import { describe, expect, it } from "vitest";
import type { VideoStats } from "@/api/desktop";
import { clampTime, formatAudio, formatBitrate, formatFrameRate, formatResolution, frameStep } from "./video";

const stats = (over: Partial<VideoStats> = {}): VideoStats => ({
  container: "mp4",
  duration: 2,
  width: 1920,
  height: 1080,
  videoCodec: "H.264",
  frameRate: 24,
  audioCodec: "AAC",
  sampleRate: 44100,
  channels: 2,
  bitrate: 1,
  tracks: 2,
  ...over,
});

describe("video helpers", () => {
  it("formats stats", () => {
    expect(formatBitrate(0)).toBe("—");
    expect(formatBitrate(800)).toBe("800 b/s");
    expect(formatBitrate(128_000)).toBe("128 kb/s");
    expect(formatBitrate(12_340_000)).toBe("12.3 Mb/s");
    expect(formatResolution(stats())).toBe("1920×1080");
    expect(formatResolution(stats({ width: 0 }))).toBe("—");
    expect(formatFrameRate(0)).toBe("—");
    expect(formatFrameRate(25)).toBe("25 fps");
    expect(formatFrameRate(29.97003)).toBe("29.97 fps");
    expect(formatAudio(stats())).toBe("AAC · 44.1 kHz · stereo");
    expect(formatAudio(stats({ channels: 1, sampleRate: 0 }))).toBe("AAC · mono");
    expect(formatAudio(stats({ channels: 6 }))).toBe("AAC · 44.1 kHz · 6 ch");
    expect(formatAudio(stats({ audioCodec: "" }))).toBe("None");
  });

  it("steps frames and clamps seeks", () => {
    expect(frameStep(stats())).toBeCloseTo(1 / 24);
    expect(frameStep(null)).toBeCloseTo(1 / 30);
    expect(frameStep(stats({ frameRate: 0 }))).toBeCloseTo(1 / 30);
    expect(clampTime(-1, 5)).toBe(0);
    expect(clampTime(9, 5)).toBe(5);
    expect(clampTime(3, 0)).toBe(3);
  });
});
