import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PushChecks, RepoChanged } from "@/api/desktop";

const api = vi.hoisted(() => ({ pushChecks: vi.fn(), onRepoChanged: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { hasPushIssues, hasPushReport, PUSH_CHECKS_DEBOUNCE_MS, usePushChecks } from "./usePushChecks";

const none: PushChecks = { violations: [], broken: [], newlyUnused: [], warnings: [], truncated: false, scanned: 0 };
let changed: (c: RepoChanged) => void = () => {};
const off = vi.fn();

let client: QueryClient;
const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;

beforeEach(() => {
  vi.resetAllMocks();
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.useFakeTimers({ shouldAdvanceTime: true });
  api.pushChecks.mockResolvedValue(none);
  api.onRepoChanged.mockImplementation((fn: (c: RepoChanged) => void) => {
    changed = fn;
    return off;
  });
});

afterEach(() => {
  vi.useRealTimers();
});

describe("hasPushIssues / hasPushReport", () => {
  it("classifies reports", () => {
    expect(hasPushIssues(undefined)).toBe(false);
    expect(hasPushIssues(none)).toBe(false);
    expect(hasPushIssues({ ...none, newlyUnused: ["a"] })).toBe(true);
    expect(hasPushReport(undefined)).toBe(false);
    expect(hasPushReport({ ...none, warnings: ["w"] })).toBe(true);
    expect(hasPushReport(none)).toBe(false);
    expect(hasPushReport({ ...none, truncated: true, scanned: 5000 })).toBe(true);
  });
});

describe("usePushChecks", () => {
  it("stays idle with nothing staged", async () => {
    renderHook(() => usePushChecks("/r", []), { wrapper });
    await act(() => vi.advanceTimersByTimeAsync(PUSH_CHECKS_DEBOUNCE_MS * 2));
    expect(api.pushChecks).not.toHaveBeenCalled();
  });

  it("debounces staged changes and refetches on repo:changed", async () => {
    const { result, rerender, unmount } = renderHook(({ staged }) => usePushChecks("/r", staged), { wrapper, initialProps: { staged: ["b", "a"] } });
    await waitFor(() => expect(result.current.data).toEqual(none));
    expect(api.pushChecks).toHaveBeenCalledTimes(1);

    rerender({ staged: ["a", "b"] });
    await act(() => vi.advanceTimersByTimeAsync(PUSH_CHECKS_DEBOUNCE_MS));
    expect(api.pushChecks).toHaveBeenCalledTimes(1);

    rerender({ staged: ["a"] });
    await act(() => vi.advanceTimersByTimeAsync(PUSH_CHECKS_DEBOUNCE_MS / 2));
    rerender({ staged: ["a", "c"] });
    await act(() => vi.advanceTimersByTimeAsync(PUSH_CHECKS_DEBOUNCE_MS / 2));
    expect(api.pushChecks).toHaveBeenCalledTimes(1);
    await act(() => vi.advanceTimersByTimeAsync(PUSH_CHECKS_DEBOUNCE_MS));
    await waitFor(() => expect(api.pushChecks).toHaveBeenCalledTimes(2));

    act(() => changed({ root: "/other", paths: [] }));
    act(() => changed({ root: "/r", paths: ["a"] }));
    act(() => changed({ root: "/r", paths: ["c"] }));
    expect(api.pushChecks).toHaveBeenCalledTimes(2);
    await act(() => vi.advanceTimersByTimeAsync(PUSH_CHECKS_DEBOUNCE_MS));
    await waitFor(() => expect(api.pushChecks).toHaveBeenCalledTimes(3));

    unmount();
    expect(off).toHaveBeenCalled();
  });
});
