import type { ReactNode } from "react";
import { TriangleAlert } from "lucide-react";
import { cn } from "@/lib/utils";

export function Notice({ children }: { children: ReactNode }) {
  return (
    <p className="flex items-start gap-1.5 text-warning">
      <TriangleAlert className="mt-px size-3 shrink-0" />
      <span>{children}</span>
    </p>
  );
}

export function Group({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex rounded-md border border-border p-0.5" role="group" aria-label={label}>
      {children}
    </div>
  );
}

export function ToolButton({ active, label, onClick, children }: { active?: boolean; label: string; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-pressed={active}
      aria-label={label}
      title={label}
      onClick={onClick}
      className={cn("flex h-6 min-w-6 items-center justify-center rounded px-1 text-muted-foreground hover:text-foreground", active && "bg-muted text-foreground")}
    >
      {children}
    </button>
  );
}
