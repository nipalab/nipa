import { useState } from "react";
import { CalendarClock, Construction, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { MaintenanceState } from "@/hooks/useMaintenance";
import { formatLocal, headline, modeDescription, type MaintenanceWindow } from "@/lib/maintenance";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

const key = (w: MaintenanceWindow) => `${w.scope}:${w.mode}:${w.startsAt}:${w.endsAt}`;

export function MaintenanceBanner({ state }: { state: MaintenanceState }) {
  const { t } = useTranslation();
  const [dismissed, setDismissed] = useState("");
  const w = state.active ?? state.upcoming;
  if (!w || (!w.active && dismissed === key(w))) return null;
  const tone = !w.active ? "bg-accent-soft" : w.mode === "read_only" ? "bg-warning/15" : "bg-danger-soft";
  const Icon = w.active ? Construction : CalendarClock;
  return (
    <div role={w.active ? "alert" : "status"} className={cn("flex shrink-0 items-start gap-2 border-b border-border px-5 py-2 text-[12px]", tone)}>
      <Icon className={cn("mt-0.5 size-3.5 shrink-0", w.active ? (w.mode === "read_only" ? "text-warning" : "text-danger") : "text-accent")} />
      <div className="min-w-0 flex-1 space-y-0.5">
        <p className="font-medium">{headline(w)}</p>
        <p className="text-muted-foreground">{modeDescription(w.mode)}</p>
        {w.message && <p>{w.message}</p>}
        <p className="text-muted-foreground">
          {w.active && w.startsAt && t("maintenance.since", { start: formatLocal(w.startsAt) })}
          {w.active && w.startsAt && w.endsAt && " · "}
          {w.endsAt && t("maintenance.estimatedEnd", { end: formatLocal(w.endsAt) })}
        </p>
      </div>
      {!w.active && (
        <Button variant="ghost" size="icon" aria-label={t("maintenance.dismiss")} onClick={() => setDismissed(key(w))}>
          <X />
        </Button>
      )}
    </div>
  );
}
