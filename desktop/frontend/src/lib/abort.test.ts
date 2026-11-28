import { describe, expect, it, vi } from "vitest";
import { withAbort } from "./abort";

function pending<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const p = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  }) as Promise<T> & { cancel?: () => void };
  p.cancel = vi.fn();
  return { p, resolve, reject };
}

describe("withAbort", () => {
  it("cancels the call when the signal aborts", () => {
    const { p } = pending<number>();
    const ctl = new AbortController();
    expect(withAbort(ctl.signal, p)).toBe(p);
    expect(p.cancel).not.toHaveBeenCalled();
    ctl.abort();
    expect(p.cancel).toHaveBeenCalledTimes(1);
  });

  it("cancels at once when the signal already aborted", () => {
    const { p } = pending<number>();
    const ctl = new AbortController();
    ctl.abort();
    withAbort(ctl.signal, p);
    expect(p.cancel).toHaveBeenCalledTimes(1);
  });

  it("stops listening once the call settles", async () => {
    const ok = pending<number>();
    const failed = pending<number>();
    const ctl = new AbortController();
    withAbort(ctl.signal, ok.p);
    withAbort(ctl.signal, failed.p).catch(() => undefined);
    ok.resolve(1);
    failed.reject(new Error("boom"));
    await expect(ok.p).resolves.toBe(1);
    await expect(failed.p).rejects.toThrow("boom");
    ctl.abort();
    expect(ok.p.cancel).not.toHaveBeenCalled();
    expect(failed.p.cancel).not.toHaveBeenCalled();
  });

  it("passes plain promises and missing signals through", async () => {
    const plain = Promise.resolve(2);
    const ctl = new AbortController();
    expect(withAbort(ctl.signal, plain)).toBe(plain);
    ctl.abort();
    await expect(plain).resolves.toBe(2);
    const { p } = pending<number>();
    expect(withAbort(undefined, p)).toBe(p);
  });
});
