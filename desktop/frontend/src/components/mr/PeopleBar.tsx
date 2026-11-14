import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { desktop, type Activity, type MergeRequest, type Person } from "@/api/desktop";
import { Avatar } from "@/components/people/Avatar";
import { MemberPicker } from "@/components/people/MemberPicker";
import { personNames } from "@/lib/mergeRequests";

type Props = {
  root: string;
  request: MergeRequest;
  outstanding: string[];
  activity?: Activity;
  busy: boolean;
  onAssign: (ids: string[]) => void;
  onRequestReview: (person: Person) => void;
};

// PeopleBar shows who a request is assigned to and who was asked to review,
// and lets the author (or any writer) change both while it is open.
export function PeopleBar({ root, request, outstanding, activity, busy, onAssign, onRequestReview }: Props) {
  const open = request.status === "open";
  const [assignees, setAssignees] = useState(request.assignees);
  const key = request.assignees.map((p) => p.id).join(",");
  useEffect(() => setAssignees(request.assignees), [key]);
  const everyone = useQuery({ queryKey: ["members", root, ""], queryFn: () => desktop.members(root, ""), staleTime: 30_000, enabled: outstanding.length > 0 });
  const names = personNames(activity, everyone.data);
  const asked = outstanding.map((id) => ({ id, name: names[id] ?? id }));

  return (
    <div className="flex flex-wrap items-start gap-x-6 gap-y-2 pt-1 text-[11px]">
      <div className="min-w-48 flex-1 space-y-1">
        <span className="text-muted-foreground">Assignees</span>
        {open ? (
          <MemberPicker
            root={root}
            label="Assignees"
            placeholder="Assign someone"
            selected={assignees}
            disabled={busy}
            onChange={(next) => {
              setAssignees(next);
              onAssign(next.map((p) => p.id));
            }}
          />
        ) : (
          <Chips people={request.assignees} empty="Nobody" />
        )}
      </div>
      <div className="min-w-48 flex-1 space-y-1">
        <span className="text-muted-foreground">Review requested from</span>
        <Chips people={asked} empty="Nobody yet" />
        {open && (
          <MemberPicker
            root={root}
            label="Request review"
            placeholder="Ask someone to review"
            selected={[]}
            exclude={[request.authorId, ...outstanding]}
            disabled={busy}
            onChange={(next) => {
              const person = next[next.length - 1];
              if (person) onRequestReview(person);
            }}
          />
        )}
      </div>
    </div>
  );
}

function Chips({ people, empty }: { people: Person[]; empty: string }) {
  if (people.length === 0) return <p className="text-muted-foreground">{empty}</p>;
  return (
    <ul className="flex flex-wrap gap-1">
      {people.map((p) => (
        <li key={p.id} className="inline-flex items-center gap-1 rounded-full bg-muted py-0.5 pl-0.5 pr-2 text-[11px]">
          <Avatar name={p.name} id={p.id} className="size-4 text-[8px]" />
          {p.name}
        </li>
      ))}
    </ul>
  );
}
