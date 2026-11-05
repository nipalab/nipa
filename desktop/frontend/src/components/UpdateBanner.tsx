import { useState } from "react";
import { Download, RotateCw, X } from "lucide-react";
import { desktop } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { describeUpdate, useUpdates } from "@/hooks/useUpdates";
import { errorMessage } from "@/lib/utils";

export function UpdateBanner({ notify }: { notify: (message: string) => void }) {
  const { state, install, restart } = useUpdates();
  const [dismissed, setDismissed] = useState("");
  if (!state?.release || dismissed === state.release.version) return null;
  if (state.phase !== "available" && state.phase !== "ready") return null;
  const { text, action, actionLabel } = describeUpdate(state);
  const fail = (err: unknown) => notify(errorMessage(err));
  const run = () => {
    if (action === "restart") restart.mutate(undefined, { onError: fail });
    else if (action === "install") install.mutate(undefined, { onError: fail });
    else desktop.openUpdatePage().catch(fail);
  };
  return (
    <div role="status" className="flex items-center gap-3 border-b border-border bg-accent-soft px-4 py-1.5 text-xs">
      <span className="min-w-0 flex-1">{text}</span>
      <Button size="sm" variant={action === "restart" ? "primary" : undefined} onClick={run} disabled={install.isPending || restart.isPending}>
        {action === "restart" ? <RotateCw /> : <Download />}
        {actionLabel}
      </Button>
      <Button variant="ghost" size="icon" aria-label="Dismiss update" onClick={() => setDismissed(state.release?.version ?? "")}>
        <X />
      </Button>
    </div>
  );
}
