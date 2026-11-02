import { X } from "lucide-react";

export function Toast({ message, onDismiss }: { message: string; onDismiss: () => void }) {
  return (
    <div role="alert" className="fixed bottom-4 right-4 z-50 flex max-w-sm items-start gap-3 rounded-lg border border-border bg-card p-3 text-xs shadow-lg">
      <span className="mt-0.5 size-2 shrink-0 rounded-full bg-danger" />
      <p className="flex-1 select-text">{message}</p>
      <button type="button" onClick={onDismiss} aria-label="Dismiss" className="text-muted-foreground hover:text-foreground">
        <X className="size-3.5" />
      </button>
    </div>
  );
}
