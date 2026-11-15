import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { desktop, type UpdateState } from "@/api/desktop";
import { translate } from "@/i18n";

export const updateKey = ["update"] as const;

// useUpdates follows the app's update state; the service pushes every change
// through the update:state event.
export function useUpdates() {
  const queryClient = useQueryClient();
  const state = useQuery({ queryKey: updateKey, queryFn: desktop.updateStatus, staleTime: Infinity });
  useEffect(() => desktop.onUpdate((s) => queryClient.setQueryData<UpdateState>(updateKey, s)), [queryClient]);
  const store = (s: UpdateState) => queryClient.setQueryData<UpdateState>(updateKey, s);
  const check = useMutation({ mutationFn: desktop.checkForUpdate, onSuccess: store });
  const install = useMutation({ mutationFn: desktop.installUpdate, onSuccess: store });
  const restart = useMutation({ mutationFn: desktop.restartToUpdate });
  return { state: state.data, check, install, restart };
}

export function downloadPercent(s: UpdateState): number {
  if (s.total <= 0) return 0;
  return Math.min(100, Math.round((s.written / s.total) * 100));
}

export type UpdateAction = "check" | "install" | "open" | "restart";

export function describeUpdate(s: UpdateState): { text: string; action: UpdateAction | null; actionLabel: string } {
  const next = s.release?.version ?? "";
  switch (s.phase) {
    case "checking":
      return { text: translate("useUpdates.checkingForUpdates"), action: null, actionLabel: "" };
    case "available":
      return s.inPlace
        ? { text: translate("useUpdates.nipaIsAvailable", { next }), action: "install", actionLabel: translate("useUpdates.download") }
        : { text: translate("useUpdates.nipaIsAvailable", { next }), action: "open", actionLabel: translate("useUpdates.getItFromTheWebsite") };
    case "downloading":
      return { text: translate("useUpdates.downloadingNipa", { next, value: downloadPercent(s) }), action: null, actionLabel: "" };
    case "ready":
      return { text: translate("useUpdates.nipaIsReady", { next }), action: "restart", actionLabel: translate("useUpdates.restartToUpdate") };
    case "upToDate":
      return { text: translate("useUpdates.nipaIsUpToDate", { current: s.current }), action: "check", actionLabel: translate("useUpdates.checkNow") };
    case "error":
      return s.release
        ? { text: translate("useUpdates.couldNotDownloadNipa", { next, error: s.error }), action: s.inPlace ? "install" : "open", actionLabel: translate("useUpdates.tryAgain") }
        : { text: translate("useUpdates.couldNotCheckForUpdates", { error: s.error }), action: "check", actionLabel: translate("useUpdates.tryAgain") };
    default:
      return { text: translate("useUpdates.nipa", { current: s.current }), action: "check", actionLabel: translate("useUpdates.checkNow") };
  }
}
