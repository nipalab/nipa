import { act, fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AudioSide, AudioStats, FileDiff } from "@/api/desktop";
import type { AudioContextLike } from "@/lib/audio";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ audioDiff: vi.fn(), hasMedia: vi.fn(), releaseMedia: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { AudioCompareView } from "./AudioDiff";
import { DiffViewer } from "./DiffViewer";

const stats = (over: Partial<AudioStats> = {}): AudioStats => ({
  codec: "PCM 16-bit",
  sampleRate: 48000,
  channels: 2,
  bitDepth: 16,
  frames: 96000,
  duration: 2,
  peakDb: -1,
  rmsDb: -18,
  loudness: -16,
  clipped: 0,
  truncated: false,
  ...over,
});

const side = (over: Partial<AudioSide> = {}): AudioSide => ({
  path: "sfx/hit.wav",
  format: "wav",
  size: 4096,
  stats: stats(),
  peaks: [
    [-0.5, 0.5, -0.2, 0.2],
    [-0.4, 0.4, -0.1, 0.1],
  ],
  spectrogram: "AAECAw==",
  specColumns: 2,
  specRows: 2,
  specMinHz: 23.4,
  specMaxHz: 24000,
  playbackUrl: "/nipa-media/a1/hit.wav",
  media: "a1",
  ...over,
});

const diff: FileDiff = {
  path: "sfx/hit.wav",
  status: "modified",
  kind: "audio",
  old: { path: "sfx/hit.wav", mode: 420, size: 4096, binary: true, hash: "aa" },
  new: { path: "sfx/hit.wav", mode: 420, size: 4096, binary: true, hash: "bb" },
  additions: 0,
  deletions: 0,
  hunks: null,
};

const ctx2d = { fillStyle: "", clearRect: vi.fn(), fillRect: vi.fn(), putImageData: vi.fn() };

function fakeAudio(decodeError?: Error) {
  const started: number[] = [];
  const gains: { gain: { value: number } }[] = [];
  let ended: (() => void) | null = null;
  const ctx = {
    currentTime: 0,
    destination: {} as AudioNode,
    decodeAudioData: vi.fn(async () => {
      if (decodeError) throw decodeError;
      return { duration: 2 } as AudioBuffer;
    }),
    createBufferSource: () => {
      const s = {
        buffer: null,
        connect: vi.fn(),
        start: (_: number, at: number) => started.push(at),
        stop: vi.fn(),
        set onended(fn: (() => void) | null) {
          if (fn) ended = fn;
        },
        get onended() {
          return ended;
        },
      };
      return s;
    },
    createGain: () => {
      const g = { gain: { value: 0 }, connect: vi.fn() };
      gains.push(g);
      return g;
    },
    resume: vi.fn(async () => {}),
    close: vi.fn(async () => {}),
  };
  return { make: () => ctx as unknown as AudioContextLike, ctx, started, gains, end: () => ended?.() };
}

const fetchMock = vi.fn();
const wav = (status = 200) => ({ ok: status === 200, status, arrayBuffer: async () => new ArrayBuffer(8) });

beforeEach(() => {
  vi.resetAllMocks();
  api.hasMedia.mockResolvedValue(true);
  api.releaseMedia.mockResolvedValue(undefined);
  fetchMock.mockImplementation(async () => wav());
  vi.stubGlobal("fetch", fetchMock);
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(ctx2d as unknown as CanvasRenderingContext2D);
  vi.stubGlobal(
    "ImageData",
    class {
      constructor(
        public data: Uint8ClampedArray,
        public width: number,
        public height: number,
      ) {}
    },
  );
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("AudioCompareView", () => {
  it("draws both versions with stats and switches what is heard", async () => {
    const audio = fakeAudio();
    renderWithProviders(<AudioCompareView compare={{ old: side({ stats: stats({ duration: 1, loudness: -20, peakDb: null }) }), new: side() }} makeContext={audio.make} />);
    expect(screen.getByRole("region", { name: "Before" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "After" })).toBeInTheDocument();
    expect(screen.getAllByLabelText("Waveform", { selector: "canvas" })).toHaveLength(2);
    expect(screen.getAllByLabelText("Spectrogram", { selector: "canvas" })).toHaveLength(2);
    expect(ctx2d.putImageData).toHaveBeenCalledTimes(2);
    expect(ctx2d.fillRect).toHaveBeenCalled();
    expect(screen.getAllByText("24,000 Hz")).toHaveLength(2);

    const table = screen.getByRole("table", { name: "Audio details" });
    expect(within(table).getByText("-20.0 LUFS")).toBeInTheDocument();
    expect(within(table).getByText("+4.0 LU")).toBeInTheDocument();
    expect(within(table).getByText("+1.00 s")).toBeInTheDocument();
    expect(within(table).getByText("−∞")).toBeInTheDocument();

    await act(async () => {});
    await userEvent.click(screen.getByRole("button", { name: "Play" }));
    expect(audio.started).toEqual([0, 0]);
    expect(audio.gains.map((g) => g.gain.value)).toEqual([0, 1]);
    expect(screen.getByRole("button", { name: "Pause" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Listen to before" }));
    expect(audio.gains.map((g) => g.gain.value)).toEqual([1, 0]);
    expect(screen.getByRole("button", { name: "Listen to before" })).toHaveAttribute("aria-pressed", "true");

    audio.ctx.currentTime = 0.5;
    await act(() => new Promise((r) => requestAnimationFrame(() => r(null))));
    await userEvent.click(screen.getByRole("button", { name: "Pause" }));
    expect(screen.getByLabelText("Playback position")).toHaveTextContent("0:00.50 / 0:02.00");
    expect(screen.getAllByTestId("playhead")).toHaveLength(2);

    const lane = screen.getByTestId("lane-After");
    vi.spyOn(lane, "getBoundingClientRect").mockReturnValue({ left: 0, width: 200 } as DOMRect);
    fireEvent.click(lane, { clientX: 150 });
    expect(screen.getByLabelText("Playback position")).toHaveTextContent("0:01.50");

    await userEvent.click(screen.getByRole("button", { name: "Play" }));
    expect(audio.started.slice(2)).toEqual([1.5, 1.5]);
    act(() => audio.end());
    expect(screen.getByRole("button", { name: "Play" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(screen.getByLabelText("Playback position")).toHaveTextContent("0:00.00");

    await userEvent.click(screen.getByRole("button", { name: "Waveform" }));
    expect(screen.queryByLabelText("Spectrogram", { selector: "canvas" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Spectrogram" }));
    expect(screen.queryByLabelText("Waveform", { selector: "canvas" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Waveform and spectrogram" }));
    expect(screen.getAllByLabelText("Waveform", { selector: "canvas" })).toHaveLength(2);
  });

  it("lists problems and works without playback", async () => {
    renderWithProviders(
      <AudioCompareView
        compare={{
          old: side({ stats: null, error: "invalid audio: not a WAV file", peaks: [], playbackUrl: "", media: "" }),
          new: side({ stats: stats({ truncated: true, clipped: 1200, channels: 6 }), playbackTruncated: true, playbackUrl: "", media: "" }),
        }}
      />,
    );
    expect(screen.getByText("No waveform for this version.")).toBeInTheDocument();
    expect(screen.getByText("Before: invalid audio: not a WAV file")).toBeInTheDocument();
    expect(screen.getByText("After: only the first 0:02.00 were analysed.")).toBeInTheDocument();
    expect(screen.getByText("After: playback stops early, the file is too long to load whole.")).toBeInTheDocument();
    expect(screen.getByText("After: 1,200 samples at full scale (clipping).")).toBeInTheDocument();
    expect(screen.getAllByText(/6 channels/).length).toBeGreaterThan(0);
    expect(screen.queryByRole("group", { name: "Listen to" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Play" }));
    expect(screen.getByRole("button", { name: "Play" })).toBeInTheDocument();
  });

  it("reports when audio cannot be played", async () => {
    renderWithProviders(<AudioCompareView compare={{ old: null, new: side({ stats: stats({ channels: 1 }) }) }} makeContext={fakeAudio(new Error("decode failed")).make} />);
    expect(screen.getByRole("region", { name: "Added" })).toBeInTheDocument();
    expect(screen.getAllByText(/mono/).length).toBeGreaterThan(0);
    expect(await screen.findByText("decode failed")).toBeInTheDocument();
  });

  it("fetches playback from the media URLs and releases them on close", async () => {
    const audio = fakeAudio();
    const { unmount } = renderWithProviders(
      <AudioCompareView compare={{ old: side({ media: "a0", playbackUrl: "/nipa-media/a0/hit.wav" }), new: side() }} makeContext={audio.make} />,
    );
    await act(async () => {});
    expect(fetchMock).toHaveBeenCalledWith("/nipa-media/a0/hit.wav", { signal: expect.any(AbortSignal) });
    expect(fetchMock).toHaveBeenCalledWith("/nipa-media/a1/hit.wav", { signal: expect.any(AbortSignal) });
    expect(audio.ctx.decodeAudioData).toHaveBeenCalledWith(expect.any(ArrayBuffer));
    const signal = fetchMock.mock.calls[0][1].signal as AbortSignal;
    unmount();
    expect(signal.aborted).toBe(true);
    expect(api.releaseMedia).toHaveBeenCalledWith(["a0", "a1"]);
  });

  it("shows the fetch error while the file is still available", async () => {
    fetchMock.mockImplementation(async () => wav(500));
    renderWithProviders(<AudioCompareView compare={{ old: null, new: side() }} makeContext={fakeAudio().make} />);
    expect(await screen.findByText("the file could not be loaded (HTTP 500)")).toBeInTheDocument();
    expect(api.hasMedia).toHaveBeenCalledWith("a1");
  });

  it("reloads once when a playback file was evicted, then says it expired", async () => {
    fetchMock.mockImplementation(async () => wav(404));
    api.hasMedia.mockResolvedValue(false);
    const onExpired = vi.fn().mockReturnValueOnce(true).mockReturnValue(false);
    const first = renderWithProviders(<AudioCompareView compare={{ old: null, new: side() }} makeContext={fakeAudio().make} onExpired={onExpired} />);
    await act(async () => {});
    expect(onExpired).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(/HTTP 404/)).not.toBeInTheDocument();
    first.unmount();
    renderWithProviders(<AudioCompareView compare={{ old: null, new: side() }} makeContext={fakeAudio().make} onExpired={onExpired} />);
    expect(await screen.findByText("The playback files are no longer available, reopen the file.")).toBeInTheDocument();
  });

  it("explains a missing audio device", async () => {
    vi.stubGlobal("AudioContext", undefined);
    renderWithProviders(<AudioCompareView compare={{ old: side(), new: null }} />);
    expect(screen.getByRole("region", { name: "Deleted" })).toBeInTheDocument();
    expect(await screen.findByText("This system has no audio output for previews.")).toBeInTheDocument();
  });
});

describe("AudioDiff", () => {
  const show = () => renderWithProviders(<DiffViewer root="/work/game" diff={diff} layout="unified" onLayout={() => {}} />);

  it("loads the comparison through the daemon", async () => {
    api.audioDiff.mockResolvedValue({ old: side(), new: side() });
    show();
    expect(screen.getByText("Analysing audio…")).toBeInTheDocument();
    expect(await screen.findByRole("table", { name: "Audio details" })).toBeInTheDocument();
    expect(api.audioDiff).toHaveBeenCalledWith("/work/game", { source: "snapshot", path: "sfx/hit.wav" }, { source: "working", path: "sfx/hit.wav" });
  });

  it("reloads the comparison once when its playback file was evicted", async () => {
    api.audioDiff.mockResolvedValueOnce({ old: null, new: side({ media: "gone", playbackUrl: "/nipa-media/gone/hit.wav" }) }).mockResolvedValue({ old: null, new: side({ media: "fresh" }) });
    fetchMock.mockImplementation(async (url: string) => wav(url.includes("gone") ? 404 : 200));
    api.hasMedia.mockImplementation(async (token: string) => token !== "gone");
    vi.stubGlobal("AudioContext", undefined);
    show();
    await vi.waitFor(() => expect(api.audioDiff).toHaveBeenCalledTimes(2));
    await vi.waitFor(() => expect(api.releaseMedia).toHaveBeenCalledWith(["gone"]));
  });

  it("falls back to metadata on errors and empty results", async () => {
    api.audioDiff.mockRejectedValueOnce(new Error("daemon gone"));
    const { unmount } = show();
    expect(await screen.findByText("The audio could not be read: daemon gone")).toBeInTheDocument();
    unmount();
    api.audioDiff.mockResolvedValueOnce(null);
    show();
    expect(await screen.findByText("There is no preview for this audio file.")).toBeInTheDocument();
  });
});
