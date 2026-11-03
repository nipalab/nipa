import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { OpProgress } from "@/api/desktop";

const api = vi.hoisted(() => ({ onOpProgress: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { describeProgress, useRepoOperation } from "./useRepoOperation";

let emit: (p: OpProgress) => void = () => {};

beforeEach(() => {
  vi.resetAllMocks();
  api.onOpProgress.mockImplementation((fn: (p: OpProgress) => void) => {
    emit = fn;
    return () => {};
  });
});

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  }) as Promise<T> & { cancel?: () => void };
  return { promise, resolve, reject };
}

const progress = (p: Partial<OpProgress>): OpProgress => ({
  root: "/r",
  op: "push",
  phase: "push",
  ahead: 0,
  objectsDone: 0,
  objectsTotal: 0,
  bytesDone: 0,
  bytesTotal: 0,
  ...p,
});

describe("useRepoOperation", () => {
  it("tracks progress and reports the outcome", async () => {
    const handlers = { onDone: vi.fn(), onError: vi.fn(), onCancelled: vi.fn() };
    const { result } = renderHook(() => useRepoOperation("/r", handlers));
    const call = deferred<{ commitId: string }>();

    act(() => result.current.run("push", () => call.promise));
    expect(result.current.state).toEqual({ op: "push", cancelling: false });
    act(() => result.current.run("update", () => Promise.resolve({})));
    expect(result.current.state?.op).toBe("push");

    act(() => {
      emit(progress({ root: "/other", phase: "upload" }));
      emit(progress({ op: "update", phase: "download" }));
      emit(progress({ phase: "upload", objectsDone: 1, objectsTotal: 2 }));
    });
    expect(result.current.state?.progress?.phase).toBe("upload");

    await act(async () => call.resolve({ commitId: "c1" }));
    expect(handlers.onDone).toHaveBeenCalledWith("push", { commitId: "c1" });
    expect(result.current.state).toBeNull();
    act(() => emit(progress({})));
    expect(result.current.state).toBeNull();
  });

  it("separates cancellations from failures", async () => {
    const handlers = { onDone: vi.fn(), onError: vi.fn(), onCancelled: vi.fn() };
    const { result } = renderHook(() => useRepoOperation("/r", handlers));

    const failing = deferred<object>();
    act(() => result.current.run("update", () => failing.promise));
    act(() => result.current.cancel());
    await act(async () => failing.reject(new Error("boom")));
    expect(handlers.onError).toHaveBeenCalledWith("update", new Error("boom"));

    const cancelled = deferred<object>();
    cancelled.promise.cancel = vi.fn(() => cancelled.reject(new Error("cancelled")));
    act(() => result.current.run("push", () => cancelled.promise));
    act(() => result.current.cancel());
    await waitFor(() => expect(handlers.onCancelled).toHaveBeenCalledWith("push"));
    expect(result.current.state).toBeNull();
    act(() => result.current.cancel());
  });
});

describe("describeProgress", () => {
  it("labels each phase", () => {
    expect(describeProgress({ op: "push", cancelling: true })).toEqual({ label: "Cancelling…" });
    expect(describeProgress({ op: "update", cancelling: false })).toEqual({ label: "Updating…" });
    expect(describeProgress({ op: "push", cancelling: false, progress: progress({ phase: "queued" }) }).label).toBe("Waiting for another operation…");
    expect(describeProgress({ op: "push", cancelling: false, progress: progress({ phase: "queued", ahead: 1 }) }).label).toBe("Waiting for 1 other operation…");
    expect(describeProgress({ op: "push", cancelling: false, progress: progress({ phase: "queued", ahead: 2 }) }).label).toBe("Waiting for 2 other operations…");
    expect(describeProgress({ op: "push", cancelling: false, progress: progress({ phase: "push" }) }).label).toBe("Pushing…");
    expect(
      describeProgress({ op: "push", cancelling: false, progress: progress({ phase: "upload", objectsDone: 1, objectsTotal: 4, bytesDone: 1024, bytesTotal: 4096 }) }),
    ).toEqual({ label: "Uploading 1/4 · 1.0 KB of 4.0 KB", percent: 25 });
    expect(describeProgress({ op: "update", cancelling: false, progress: progress({ phase: "download", objectsDone: 1, objectsTotal: 2 }) })).toEqual({
      label: "Downloading 1/2",
      percent: 50,
    });
  });
});
