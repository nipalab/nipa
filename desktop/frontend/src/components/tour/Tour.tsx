import { Button } from "@/components/ui/button";
import { labels, TOUR_STEPS, useSimple, useTour, writeTour } from "@/lib/mode";

export function tourSteps(simple: boolean) {
  const { update, push } = labels(simple);
  return [
    { title: update, text: `Start each day with ${update}. It brings in what your teammates changed.` },
    { title: "Lock before editing", text: "Lock an art or audio file in the Files tab before you change it, so nobody overwrites your work." },
    { title: "Review your changes", text: "Everything you edit shows up in Changes. Compare it with the last version before you share it." },
    { title: push, text: `Write a short note and ${push} to share your work with the team.` },
  ];
}

export function Tour() {
  const state = useTour();
  const simple = useSimple();
  if (typeof state !== "number") return null;
  const steps = tourSteps(simple);
  const step = steps[state];
  const last = state === TOUR_STEPS - 1;
  return (
    <aside role="dialog" aria-label="Quick tour" className="fixed bottom-5 right-5 z-30 w-72 space-y-2 rounded-lg border border-border bg-card p-4 text-xs shadow-xl">
      <p className="text-[11px] text-muted-foreground">
        Step {state + 1} of {TOUR_STEPS}
      </p>
      <h2 className="text-sm font-semibold">{step.title}</h2>
      <p className="text-muted-foreground">{step.text}</p>
      <div className="flex justify-between pt-1">
        <Button variant="ghost" size="sm" onClick={() => writeTour("done")}>
          Skip tour
        </Button>
        <Button variant="primary" size="sm" onClick={() => writeTour(last ? "done" : state + 1)}>
          {last ? "Done" : "Next"}
        </Button>
      </div>
    </aside>
  );
}
