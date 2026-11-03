import { useEffect, useRef } from "react";
import { Button } from "@/components/ui/button";

type ConfirmProps = {
  title: string;
  message: string;
  confirmLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
};

export function ConfirmDialog({ title, message, confirmLabel, onConfirm, onCancel }: ConfirmProps) {
  const cancelRef = useRef<HTMLButtonElement>(null);
  useEffect(() => cancelRef.current?.focus(), []);
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4" onKeyDown={(e) => e.key === "Escape" && onCancel()}>
      <div role="alertdialog" aria-modal="true" aria-labelledby="confirm-title" className="w-full max-w-sm space-y-3 rounded-lg border border-border bg-card p-5 shadow-xl">
        <h2 id="confirm-title" className="text-sm font-semibold">
          {title}
        </h2>
        <p className="select-text break-words text-xs text-muted-foreground">{message}</p>
        <div className="flex justify-end gap-2 pt-1">
          <Button ref={cancelRef} size="sm" onClick={onCancel}>
            Cancel
          </Button>
          <Button size="sm" variant="primary" className="bg-danger hover:bg-danger/90" onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}
