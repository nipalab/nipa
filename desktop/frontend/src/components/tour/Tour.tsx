import { Button } from "@/components/ui/button";
import { labels, TOUR_STEPS, useSimple, useTour, writeTour } from "@/lib/mode";
import { useTranslation } from "react-i18next";
import { translate } from "@/i18n";

export function tourSteps(simple: boolean) {
  const { update, push } = labels(simple);
  return [
    { title: update, text: translate("tour.startEachDayWith", { update }) },
    { title: translate("tour.lockBeforeEditing"), text: translate("tour.lockAnArtOrAudio") },
    { title: translate("tour.reviewYourChanges"), text: translate("tour.everythingYouEditShowsUp") },
    { title: push, text: translate("tour.writeAShortNoteAnd", { push }) },
  ];
}

export function Tour() {
  const { t } = useTranslation();
  const state = useTour();
  const simple = useSimple();
  if (typeof state !== "number") return null;
  const steps = tourSteps(simple);
  const step = steps[state];
  const last = state === TOUR_STEPS - 1;
  return (
    <aside role="dialog" aria-label={t("tour.quickTour")} className="fixed bottom-5 right-5 z-30 w-72 space-y-2 rounded-lg border border-border bg-card p-4 text-xs shadow-xl">
      <p className="text-[11px] text-muted-foreground">
        {t("tour.step", { step: state + 1, total: TOUR_STEPS })}
      </p>
      <h2 className="text-sm font-semibold">{step.title}</h2>
      <p className="text-muted-foreground">{step.text}</p>
      <div className="flex justify-between pt-1">
        <Button variant="ghost" size="sm" onClick={() => writeTour("done")}>
          {t("tour.skipTour")}
        </Button>
        <Button variant="primary" size="sm" onClick={() => writeTour(last ? "done" : state + 1)}>
          {last ? translate("tour.done") : translate("tour.next")}
        </Button>
      </div>
    </aside>
  );
}
