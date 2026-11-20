import { Download, RefreshCw, RotateCw } from "lucide-react";
import { desktop } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { Group, Row, SwitchRow } from "@/components/settings/SettingsLayout";
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
    return (
      <Group>
        <p className="px-4 py-3 text-muted-foreground">{state.current ? t("updates.devBuildVersion", { version: state.current }) : t("updates.devBuild")}</p>
      </Group>
    );
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
  const notes = state.release?.notes && (state.phase === "available" || state.phase === "ready") ? state.release.notes : "";
  return (
    <>
      <Group>
        <Row
          label={<span role="status">{text}</span>}
          below={
            state.phase === "downloading" ? (
              <div className="mt-2 h-1 overflow-hidden rounded bg-muted" aria-hidden>
                <div className="h-full bg-accent transition-[width]" style={{ width: `${downloadPercent(state)}%` }} />
              </div>
            ) : notes ? (
              <p className="mt-2 max-h-48 overflow-y-auto whitespace-pre-wrap rounded-md bg-muted/60 px-3 py-2 text-muted-foreground">{notes}</p>
            ) : null
          }
        >
          {action && (
            <Button type="button" size="sm" variant={action === "restart" || action === "install" ? "primary" : undefined} onClick={() => run(action)} disabled={busy}>
              <Icon />
              {actionLabel}
            </Button>
          )}
        </Row>
      </Group>
      <Group>
        <SwitchRow
          label={state.inPlace ? translate("updatesSection.downloadUpdatesAutomatically") : translate("updatesSection.checkForUpdatesAutomatically")}
          description={state.inPlace ? t("updatesSection.autoDownloadHint") : t("updatesSection.autoCheckHint")}
          checked={auto}
          onChange={onAuto}
        />
      </Group>
    </>
  );
}
