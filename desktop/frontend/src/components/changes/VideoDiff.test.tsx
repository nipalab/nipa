import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { FileDiff, VideoSide, VideoStats } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ videoDiff: vi.fn(), openMedia: vi.fn(), hasMedia: vi.fn(), releaseMedia: vi.fn(() => Promise.resolve()) }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { DiffViewer, viewerFor } from "./DiffViewer";
import { VideoCompareView, VideoDiff } from "./VideoDiff";

const stats = (over: Partial<VideoStats> = {}): VideoStats => ({
  container: "mp4",
  duration: 2,
  width: 1280,
  height: 720,
  videoCodec: "H.264",
  frameRate: 25,
  audioCodec: "AAC",
  sampleRate: 48000,
  channels: 2,
  bitrate: 4_000_000,
  tracks: 2,
  ...over,
});

const side = (over: Partial<VideoSide> = {}): VideoSide => ({
  path: "cut/intro.mp4",
  format: "mp4",
  size: 2048,
  stats: stats(),
  statsError: "",
  url: "/nipa-media/tok/intro.mp4",
  media: "tok",
  ...over,
});

const diff: FileDiff = {
  path: "cut/intro.mp4",
  status: "modified",
  kind: "video",
  old: { path: "cut/intro.mp4", mode: 420, size: 2048, binary: true, hash: "aa" },
  new: { path: "cut/intro.mp4", mode: 420, size: 2048, binary: true, hash: "bb" },
  additions: 0,
  deletions: 0,
  hunks: null,
};

let play: ReturnType<typeof vi.fn>;
let pause: ReturnType<typeof vi.fn>;

beforeEach(() => {
  play = vi.fn(() => Promise.resolve());
  pause = vi.fn();
  Object.defineProperty(HTMLMediaElement.prototype, "play", { configurable: true, value: play });
  Object.defineProperty(HTMLMediaElement.prototype, "pause", { configurable: true, value: pause });
  Object.defineProperty(HTMLMediaElement.prototype, "duration", { configurable: true, get: () => 2 });
});

afterEach(() => vi.clearAllMocks());

describe("VideoCompareView", () => {
  it("plays both versions in sync and steps frames", async () => {
    const user = userEvent.setup();
    renderWithProviders(<VideoCompareView compare={{ old: side({ stats: stats({ width: 640, height: 360 }), media: "a" }), new: side({ media: "b" }) }} onOpen={api.openMedia} />);
    const [before, after] = [screen.getByLabelText("Before video") as HTMLVideoElement, screen.getByLabelText("After video") as HTMLVideoElement];
    expect(before.muted).toBe(true);
    expect(after.muted).toBe(false);

    await user.click(screen.getByRole("button", { name: "Play" }));
    expect(play).toHaveBeenCalledTimes(2);
    expect(screen.getByRole("button", { name: "Pause" })).toBeInTheDocument();

    after.currentTime = 1;
    before.currentTime = 0.2;
    Object.defineProperty(before, "paused", { configurable: true, get: () => false });
    fireEvent.timeUpdate(after);
    expect(before.currentTime).toBe(1);
    expect(screen.getByLabelText("Playback position")).toHaveTextContent("0:01.00 / 0:02.00");
    fireEvent.timeUpdate(before);

    await user.click(screen.getByRole("button", { name: "Pause" }));
    expect(pause).toHaveBeenCalledTimes(2);

    await user.click(screen.getByRole("button", { name: "Next frame" }));
    expect(after.currentTime).toBeCloseTo(1.04);
    await user.click(screen.getByRole("button", { name: "Previous frame" }));
    expect(after.currentTime).toBeCloseTo(1);

    fireEvent.change(screen.getByRole("slider", { name: "Seek" }), { target: { value: "1.5" } });
    expect(before.currentTime).toBe(1.5);

    await user.click(screen.getByRole("button", { name: "Listen to before" }));
    expect(before.muted).toBe(false);
    expect(after.muted).toBe(true);

    await user.click(screen.getByRole("button", { name: "Play" }));
    pause.mockClear();
    fireEvent.ended(before);
    expect(pause).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Pause" })).toBeInTheDocument();
    fireEvent.ended(after);
    expect(pause).toHaveBeenCalledTimes(2);
    expect(screen.getByRole("button", { name: "Play" })).toBeInTheDocument();

    const table = screen.getByRole("table", { name: "Video details" });
    expect(within(table).getByText("1280×720")).toHaveClass("text-warning");
    expect(within(table).getAllByText("4.0 Mb/s")).toHaveLength(2);
  });

  it("falls back to opening externally when the webview cannot play", async () => {
    const user = userEvent.setup();
    api.openMedia.mockRejectedValueOnce(new Error("no player")).mockResolvedValue(undefined);
    renderWithProviders(<VideoCompareView compare={{ old: null, new: side({ stats: stats({ videoCodec: "ProRes 422" }) }) }} onOpen={api.openMedia} />);
    expect(screen.queryByRole("group", { name: "Listen to" })).not.toBeInTheDocument();
    fireEvent.error(screen.getByLabelText("Added video"));
    expect(screen.getByText("This video's format can't play in the app (ProRes 422).")).toBeInTheDocument();
    const buttons = screen.getAllByRole("button", { name: /Open externally/ });
    await user.click(buttons[1]);
    expect(api.openMedia).toHaveBeenCalledWith("tok");
    expect(await screen.findByText("no player")).toBeInTheDocument();
    await user.click(buttons[0]);
    expect(api.openMedia).toHaveBeenCalledTimes(2);
    await user.click(screen.getByRole("button", { name: "Play" }));
    expect(play).not.toHaveBeenCalled();
  });

  it("explains files without playback or stats", () => {
    renderWithProviders(
      <VideoCompareView
        compare={{ old: side({ url: "", media: "", stats: null, statsError: "the video is too large to preview" }), new: side({ stats: null, statsError: "unsupported format: avi video" }) }}
      />,
    );
    expect(screen.getByText("the video is too large to preview")).toBeInTheDocument();
    expect(screen.getByText("Before: the video is too large to preview")).toBeInTheDocument();
    expect(screen.getByText("After: unsupported format: avi video")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /Open externally/ })).toHaveLength(1);
    expect(screen.getByLabelText("Playback position")).toHaveTextContent("0:00.00 / 0:00.00");
    fireEvent.loadedMetadata(screen.getByLabelText("After video"));
    expect(screen.getByLabelText("Playback position")).toHaveTextContent("0:00.00 / 0:02.00");
  });

  it("tolerates a play() that throws", async () => {
    Object.defineProperty(HTMLMediaElement.prototype, "play", {
      configurable: true,
      value: () => {
        throw new Error("not implemented");
      },
    });
    const user = userEvent.setup();
    renderWithProviders(<VideoCompareView compare={{ old: side(), new: null }} />);
    await act(async () => user.click(screen.getByRole("button", { name: "Play" })));
    expect(screen.getByRole("button", { name: "Pause" })).toBeInTheDocument();
  });
});

describe("VideoDiff", () => {
  const show = () => renderWithProviders(<DiffViewer root="/work/game" diff={diff} layout="unified" onLayout={() => {}} />);

  it("picks the video viewer and loads both sides", async () => {
    expect(viewerFor(diff)).toBe(VideoDiff);
    api.videoDiff.mockResolvedValue({ old: side(), new: side() });
    show();
    expect(screen.getByText("Preparing video…")).toBeInTheDocument();
    expect(await screen.findByRole("table", { name: "Video details" })).toBeInTheDocument();
    expect(api.videoDiff).toHaveBeenCalledWith("/work/game", { source: "snapshot", path: "cut/intro.mp4" }, { source: "working", path: "cut/intro.mp4" });
  });

  it("reloads once when a prepared file was evicted", async () => {
    api.videoDiff.mockResolvedValueOnce({ old: null, new: side({ media: "gone", url: "/nipa-media/gone/intro.mp4" }) });
    api.videoDiff.mockResolvedValue({ old: null, new: side({ media: "fresh", url: "/nipa-media/fresh/intro.mp4" }) });
    api.hasMedia.mockResolvedValue(false);
    show();
    fireEvent.error(await screen.findByLabelText("Added video"));
    await waitFor(() => expect(api.videoDiff).toHaveBeenCalledTimes(2));
    expect(api.hasMedia).toHaveBeenCalledWith("gone");
    await waitFor(() => expect(screen.getByLabelText("Added video")).toHaveAttribute("src", "/nipa-media/fresh/intro.mp4"));
    expect(screen.queryByText(/can't play in the app/)).not.toBeInTheDocument();

    expect(api.releaseMedia).toHaveBeenCalledWith(["gone"]);

    fireEvent.error(screen.getByLabelText("Added video"));
    expect(await screen.findByText(/can't play in the app/)).toBeInTheDocument();
    expect(api.videoDiff).toHaveBeenCalledTimes(2);
  });

  it("releases its files when the viewer closes", async () => {
    api.videoDiff.mockResolvedValue({ old: side({ media: "a" }), new: side({ media: "b" }) });
    api.releaseMedia.mockRejectedValueOnce(new Error("daemon gone"));
    const { unmount } = show();
    await screen.findByLabelText("After video");
    expect(api.releaseMedia).not.toHaveBeenCalled();
    unmount();
    expect(api.releaseMedia).toHaveBeenCalledWith(["a", "b"]);
  });

  it("reports a format problem without reloading when the file is still there", async () => {
    api.videoDiff.mockResolvedValue({ old: null, new: side() });
    api.hasMedia.mockResolvedValue(true);
    show();
    fireEvent.error(await screen.findByLabelText("Added video"));
    expect(await screen.findByText(/can't play in the app/)).toBeInTheDocument();
    expect(api.videoDiff).toHaveBeenCalledTimes(1);
  });

  it("treats a failed availability check as a playback failure", async () => {
    api.videoDiff.mockResolvedValue({ old: null, new: side() });
    api.hasMedia.mockRejectedValue(new Error("daemon gone"));
    show();
    fireEvent.error(await screen.findByLabelText("Added video"));
    expect(await screen.findByText(/can't play in the app/)).toBeInTheDocument();
  });

  it("falls back to metadata on errors and empty results", async () => {
    api.videoDiff.mockRejectedValueOnce(new Error("daemon gone"));
    const { unmount } = show();
    expect(await screen.findByText("The video could not be read: daemon gone")).toBeInTheDocument();
    unmount();
    api.videoDiff.mockResolvedValueOnce(null);
    show();
    expect(await screen.findByText("There is no preview for this video.")).toBeInTheDocument();
  });
});
