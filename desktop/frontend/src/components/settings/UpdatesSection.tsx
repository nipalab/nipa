import { Download, RefreshCw, RotateCw } from "lucide-react";
import { desktop } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { describeUpdate, downloadPercent, useUpdates, type UpdateAction } from "@/hooks/useUpdates";
import { errorMessage } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { translate } from "@/i18n";

type Props = {
  auto: boolean;
  onAuto: (auto: boolean) => void;
  notify: (message: string, tone?: "error" | "success") => void;
};

export function UpdatesSection({ auto, onAuto, notify }: Props) {
  const { t } = useTranslation();
  const { state, check, install, restart } = useUpdates();
  if (!state) return null;
  if (!state.enabled) {
    return <p className="text-muted-foreground">{state.current ? t("updates.devBuildVersion", { version: state.current }) : t("updates.devBuild")}</p>;
  }
  const { text, action, actionLabel } = describeUpdate(state);
  const busy = check.isPending || install.isPending || restart.isPending;
  const run = (a: UpdateAction) => {
    const fail = (err: unknown) => notify(errorMessage(err));
    if (a === "check") check.mutate(undefined, { onError: fail });
    if (a === "install") install.mutate(undefined, { onError: fail });
    if (a === "restart") restart.mutate(undefined, { onError: fail });
    if (a === "open") desktop.openUpdatePage().catch(fail);
  };
  const Icon = action === "restart" ? RotateCw : action === "check" ? RefreshCw : Download;
  return (
    <>
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={auto} onChange={(e) => onAuto(e.target.checked)} className="size-3.5 accent-[var(--accent)]" />
        {state.inPlace ? translate("updatesSection.downloadUpdatesAutomatically") : translate("updatesSection.checkForUpdatesAutomatically")}
      </label>
      <div className="flex items-center justify-between gap-3">
        <span role="status" className="min-w-0">
          {text}
        </span>
        {action && (
          <Button type="button" size="sm" variant={action === "restart" ? "primary" : undefined} onClick={() => run(action)} disabled={busy}>
            <Icon />
            {actionLabel}
          </Button>
        )}
      </div>
      {state.phase === "downloading" && (
        <div className="h-1 overflow-hidden rounded bg-muted" aria-hidden>
          <div className="h-full bg-accent" style={{ width: `${downloadPercent(state)}%` }} />
        </div>
      )}
      {state.release?.notes && (state.phase === "available" || state.phase === "ready") && (
        <p className="whitespace-pre-wrap text-muted-foreground">{state.release.notes}</p>
      )}
    </>
  );
}
