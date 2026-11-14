import { useCallback, useEffect, useRef, useState } from "react";
import { desktop, type Cancellable, type OpOutcome, type OpProgress } from "@/api/desktop";
import { formatBytes } from "@/lib/diff";
import { translate } from "@/i18n";

export type OpName = "push" | "update" | "sparse" | "clone" | "switch" | "revert" | "merge" | "restore" | "shelve";

export type OpState = { op: OpName; progress?: OpProgress; cancelling: boolean };

type Handlers = {
  onDone: (op: OpName, outcome: OpOutcome) => void;
  onError: (op: OpName, err: unknown) => void;
  onCancelled: (op: OpName) => void;
};

// useRepoOperation runs one push or update at a time for a clone and follows
// its progress events.
export function useRepoOperation(root: string, handlers: Handlers) {
  const [state, setState] = useState<OpState | null>(null);
  const pending = useRef<{ call: Cancellable<OpOutcome>; cancelled: boolean } | null>(null);
  const latest = useRef(handlers);
  latest.current = handlers;

  useEffect(
    () =>
      desktop.onOpProgress((p) => {
        if (p.root !== root) return;
        setState((s) => (s && s.op === p.op ? { ...s, progress: p } : s));
      }),
    [root],
  );

  const run = useCallback((op: OpName, start: () => Cancellable<OpOutcome>) => {
    if (pending.current) return;
    const entry = { call: start(), cancelled: false };
    pending.current = entry;
    setState({ op, cancelling: false });
    entry.call
      .then(
        (outcome) => latest.current.onDone(op, outcome),
        (err) => (entry.cancelled ? latest.current.onCancelled(op) : latest.current.onError(op, err)),
      )
      .finally(() => {
        pending.current = null;
        setState(null);
      });
  }, []);

  const cancel = useCallback(() => {
    const entry = pending.current;
    if (!entry?.call.cancel) return;
    entry.cancelled = true;
    setState((s) => (s ? { ...s, cancelling: true } : s));
    entry.call.cancel();
  }, []);

  return { state, run, cancel };
}

const verbs: Record<OpName, string> = { push: translate("useRepoOperation.pushing"), update: translate("useRepoOperation.updating"), sparse: translate("useRepoOperation.updatingCheckout"), clone: translate("useRepoOperation.cloning"), switch: translate("useRepoOperation.switching"), revert: translate("useRepoOperation.reverting"), merge: translate("useRepoOperation.merging"), restore: translate("useRepoOperation.gettingVersion"), shelve: translate("useRepoOperation.shelving") };

export function describeProgress(state: OpState): { label: string; percent?: number } {
  const p = state.progress;
  if (state.cancelling) return { label: translate("useRepoOperation.cancelling") };
  if (!p) return { label: translate("useRepoOperation.text", { value: verbs[state.op] }) };
  if (p.phase === "queued") {
    return { label: p.ahead > 0 ? translate("repoOperation.waiting", { count: p.ahead }) : translate("useRepoOperation.waitingForAnotherOperation") };
  }
  if (p.phase === "verify" && p.objectsTotal > 0) {
    return { label: translate("repoOperation.verifying", { count: p.objectsTotal }) };
  }
  if ((p.phase === "upload" || p.phase === "download") && p.objectsTotal > 0) {
    const verb = p.phase === "upload" ? translate("useRepoOperation.uploading") : translate("useRepoOperation.downloading");
    const bytes = p.bytesTotal > 0 ? translate("useRepoOperation.of", { value: formatBytes(p.bytesDone), value2: formatBytes(p.bytesTotal) }) : "";
    const ratio = p.bytesTotal > 0 ? p.bytesDone / p.bytesTotal : p.objectsDone / p.objectsTotal;
    return { label: translate("useRepoOperation.text2", { verb, objectsDone: p.objectsDone, objectsTotal: p.objectsTotal, bytes }), percent: Math.min(100, Math.round(ratio * 100)) };
  }
  return { label: translate("useRepoOperation.text", { value: verbs[state.op] }) };
}
