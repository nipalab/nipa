import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { desktop, type UpdateState } from "@/api/desktop";

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
      return { text: "Checking for updates…", action: null, actionLabel: "" };
    case "available":
      return s.inPlace
        ? { text: `Nipa ${next} is available.`, action: "install", actionLabel: "Download" }
        : { text: `Nipa ${next} is available.`, action: "open", actionLabel: "Get it from the website" };
    case "downloading":
      return { text: `Downloading Nipa ${next}… ${downloadPercent(s)}%`, action: null, actionLabel: "" };
    case "ready":
      return { text: `Nipa ${next} is ready.`, action: "restart", actionLabel: "Restart to update" };
    case "upToDate":
      return { text: `Nipa ${s.current} is up to date.`, action: "check", actionLabel: "Check now" };
    case "error":
      return s.release
        ? { text: `Could not download Nipa ${next}: ${s.error}`, action: s.inPlace ? "install" : "open", actionLabel: "Try again" }
        : { text: `Could not check for updates: ${s.error}`, action: "check", actionLabel: "Try again" };
    default:
      return { text: `Nipa ${s.current}`, action: "check", actionLabel: "Check now" };
  }
}
