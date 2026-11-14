import { Brush, Wrench } from "lucide-react";
import { Dialog } from "@/components/ui/dialog";
import { setMode, type Mode } from "@/lib/mode";

const choices: { mode: Mode; title: string; detail: string; Icon: typeof Brush }[] = [
  { mode: "simple", title: "I mostly work on art, audio or design", detail: "Fewer tabs and plain words: Get latest, review, Submit.", Icon: Brush },
  { mode: "full", title: "I work with code or manage the project", detail: "Everything: branches, tags, merge requests and more.", Icon: Wrench },
];

export function ModePrompt() {
  return (
    <Dialog title="How do you use Nipa?" onClose={() => setMode("full")}>
      <p className="text-xs text-muted-foreground">You can change this any time in Settings.</p>
      <div className="space-y-2">
        {choices.map(({ mode, title, detail, Icon }) => (
          <button key={mode} type="button" onClick={() => setMode(mode)} className="flex w-full items-start gap-3 rounded-md border border-border p-3 text-left hover:bg-muted">
            <Icon className="mt-0.5 size-4 shrink-0 text-accent" />
            <span className="space-y-0.5">
              <span className="block text-[13px] font-medium">{title}</span>
              <span className="block text-xs text-muted-foreground">{detail}</span>
            </span>
          </button>
        ))}
      </div>
    </Dialog>
  );
}
