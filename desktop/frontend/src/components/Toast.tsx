import { useEffect } from "react";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";

const SUCCESS_TIMEOUT_MS = 4000;

type ToastProps = { message: string; tone?: "error" | "success"; onDismiss: () => void };

export function Toast({ message, tone = "error", onDismiss }: ToastProps) {
  useEffect(() => {
    if (tone !== "success") return;
    const timer = setTimeout(onDismiss, SUCCESS_TIMEOUT_MS);
    return () => clearTimeout(timer);
  }, [tone, message, onDismiss]);

  return (
    <div
      role={tone === "error" ? "alert" : "status"}
      className="fixed bottom-4 right-4 z-50 flex max-w-sm items-start gap-3 rounded-lg border border-border bg-card p-3 text-xs shadow-lg"
    >
      <span className={cn("mt-0.5 size-2 shrink-0 rounded-full", tone === "error" ? "bg-danger" : "bg-success")} />
      <p className="flex-1 select-text">{message}</p>
      <button type="button" onClick={onDismiss} aria-label="Dismiss" className="text-muted-foreground hover:text-foreground">
        <X className="size-3.5" />
      </button>
    </div>
  );
}
