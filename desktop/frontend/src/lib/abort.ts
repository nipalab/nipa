// withAbort ties a cancellable Wails call to a react-query abort signal:
// when the query is abandoned (the selection moved on, the view unmounted)
// the call's cancel() cancels the Go context, which stops the daemon work.
// Passing the signal also tells react-query the query is abortable.
export function withAbort<P extends Promise<unknown> & { cancel?: () => void }>(signal: AbortSignal | undefined, p: P): P {
  if (!signal || !p.cancel) return p;
  const cancel = () => p.cancel?.();
  if (signal.aborted) {
    cancel();
    return p;
  }
  signal.addEventListener("abort", cancel, { once: true });
  const detach = () => signal.removeEventListener("abort", cancel);
  p.then(detach, detach);
  return p;
}
