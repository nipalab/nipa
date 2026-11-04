import { describe, expect, it, vi } from "vitest";
import { colormap, createPlayer, drawWaveform, formatDb, formatTime, spectrogramPixels, type AudioContextLike } from "./audio";

describe("audio helpers", () => {
  it("maps levels onto the colour ramp", () => {
    expect(colormap(0)).toEqual([0, 0, 4]);
    expect(colormap(255)).toEqual([252, 255, 164]);
    expect(colormap(-5)).toEqual([0, 0, 4]);
    expect(colormap(999)).toEqual([252, 255, 164]);
    const mid = colormap(128);
    expect(mid[0]).toBeGreaterThan(150);
  });

  it("puts the highest frequency row on top", () => {
    const px = spectrogramPixels(new Uint8Array([0, 0, 255, 255]), 2, 2);
    expect([...px.slice(0, 4)]).toEqual([252, 255, 164, 255]);
    expect([...px.slice(8, 12)]).toEqual([0, 0, 4, 255]);
    expect(spectrogramPixels(new Uint8Array([]), 1, 1)[3]).toBe(255);
  });

  it("draws one column per pixel spanning min to max", () => {
    const ctx = { fillStyle: "", clearRect: vi.fn(), fillRect: vi.fn() };
    drawWaveform(ctx, [-1, 1, -0.5, 0.5, 0, 0, 0, 0], 2, 100, "red", 10);
    expect(ctx.fillStyle).toBe("red");
    expect(ctx.fillRect.mock.calls).toEqual([
      [0, 60, 2, 1],
      [0, 10, 1, 100],
      [1, 60, 1, 1],
    ]);
    ctx.fillRect.mockClear();
    drawWaveform(ctx, [-0.5, 0.5], 4, 100, "red");
    expect(ctx.fillRect).toHaveBeenCalledTimes(5);
    ctx.fillRect.mockClear();
    drawWaveform(ctx, [], 4, 100, "red");
    expect(ctx.fillRect).toHaveBeenCalledTimes(1);
  });

  it("formats times and levels", () => {
    expect(formatTime(0)).toBe("0:00.00");
    expect(formatTime(65.256)).toBe("1:05.26");
    expect(formatTime(-3)).toBe("0:00.00");
    expect(formatDb(-6.02)).toBe("-6.0 dBFS");
    expect(formatDb(null)).toBe("−∞");
    expect(formatDb(-14, "LUFS")).toBe("-14.0 LUFS");
  });
});

type FakeSource = { buffer: AudioBuffer | null; onended: (() => void) | null; connect: ReturnType<typeof vi.fn>; start: ReturnType<typeof vi.fn>; stop: ReturnType<typeof vi.fn> };

function fakeContext() {
  const sources: FakeSource[] = [];
  const gains: { gain: { value: number }; connect: ReturnType<typeof vi.fn> }[] = [];
  const ctx = {
    currentTime: 10,
    destination: {} as AudioNode,
    decodeAudioData: vi.fn(async (data: ArrayBuffer) => ({ duration: data.byteLength }) as AudioBuffer),
    createBufferSource: vi.fn(() => {
      const s: FakeSource = { buffer: null, onended: null, connect: vi.fn(), start: vi.fn(), stop: vi.fn() };
      sources.push(s);
      return s;
    }),
    createGain: vi.fn(() => {
      const g = { gain: { value: -1 }, connect: vi.fn() };
      gains.push(g);
      return g;
    }),
    resume: vi.fn(async () => {}),
    close: vi.fn(async () => {}),
  };
  return { ctx: ctx as AudioContextLike & typeof ctx, sources, gains };
}

describe("createPlayer", () => {
  it("plays both sides in sync and flips which one is audible", async () => {
    const { ctx, sources, gains } = fakeContext();
    const ended = vi.fn();
    const p = createPlayer(() => ctx, ended);
    p.play(1);
    expect(sources).toHaveLength(0);
    await p.load(new Uint8Array(4), new Uint8Array(6));
    expect(p.duration()).toBe(6);

    p.play(1);
    expect(p.playing()).toBe(true);
    expect(sources.map((s) => s.start.mock.calls[0])).toEqual([
      [0, 1],
      [0, 1],
    ]);
    expect(gains.map((g) => g.gain.value)).toEqual([0, 1]);
    ctx.currentTime = 12;
    expect(p.position()).toBe(3);

    p.setSide("old");
    expect(gains.map((g) => g.gain.value)).toEqual([1, 0]);

    p.pause();
    expect(p.playing()).toBe(false);
    expect(p.position()).toBe(3);
    expect(sources[0].stop).toHaveBeenCalled();
    p.pause();

    p.seek(5);
    expect(p.position()).toBe(5);
    expect(p.playing()).toBe(false);
    p.play(5);
    expect(sources).toHaveLength(3);
    expect(gains[2].gain.value).toBe(0);
    p.seek(2);
    expect(sources).toHaveLength(5);

    sources[4].onended?.();
    expect(ended).toHaveBeenCalled();
    expect(p.playing()).toBe(false);
    expect(p.position()).toBe(0);

    p.seek(99);
    expect(p.position()).toBe(6);
    p.dispose();
    expect(ctx.close).toHaveBeenCalled();
    p.play(0);
  });

  it("falls back to the only version there is", async () => {
    const { ctx, sources } = fakeContext();
    const p = createPlayer(() => ctx);
    await p.load(new Uint8Array(3), null);
    p.play(0);
    expect(sources).toHaveLength(1);
    sources[0].onended?.();
    p.play(10);
    expect(p.playing()).toBe(false);
    expect(p.position()).toBe(3);
  });
});
