import { AlertTriangle, Loader2, UserRound } from "lucide-react";
import type { TextTools } from "@/hooks/useTextTools";
import { cn, errorMessage } from "@/lib/utils";

export function BlameToggle({ tools }: { tools: TextTools }) {
  if (!tools.available) return null;
  const title = tools.error ? `Blame is not available: ${errorMessage(tools.error)}` : tools.on ? "Hide who last changed each line" : "Show who last changed each line";
  return (
    <button
      type="button"
      aria-pressed={tools.on}
      title={title}
      onClick={tools.toggle}
      className={cn("flex h-6 shrink-0 items-center gap-1 rounded-md border border-border px-1.5 text-[11px] text-muted-foreground hover:text-foreground", tools.on && "bg-muted text-foreground")}
    >
      {tools.loading ? <Loader2 className="size-3 animate-spin" /> : tools.error ? <AlertTriangle className="size-3 text-danger" /> : <UserRound className="size-3" />}
      Blame
    </button>
  );
}
