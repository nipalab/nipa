import { useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";

type PromptProps = {
  title: string;
  label: string;
  confirmLabel: string;
  note?: string;
  placeholder?: string;
  withMessage?: boolean;
  busy?: boolean;
  onConfirm: (name: string, message: string) => void;
  onCancel: () => void;
};

// PromptDialog asks for a name, and optionally a message, before creating a
// branch or a tag.
export function PromptDialog({ title, label, confirmLabel, note, placeholder, withMessage, busy, onConfirm, onCancel }: PromptProps) {
  const [name, setName] = useState("");
  const [message, setMessage] = useState("");
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (name.trim()) onConfirm(name.trim(), message.trim());
  };
  return (
    <div role="presentation" className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4" onKeyDown={(e) => e.key === "Escape" && onCancel()}>
      <form role="dialog" aria-modal="true" aria-labelledby="prompt-title" onSubmit={submit} className="w-full max-w-sm space-y-3 rounded-lg border border-border bg-card p-5 shadow-xl">
        <h2 id="prompt-title" className="text-sm font-semibold">
          {title}
        </h2>
        {note && <p className="break-words text-xs text-muted-foreground">{note}</p>}
        <label className="block space-y-1 text-[12px]">
          <span className="text-muted-foreground">{label}</span>
          <input
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={placeholder}
            className="w-full rounded-md border border-border bg-background px-2 py-1.5 font-mono text-[12px] outline-none focus:border-accent"
          />
        </label>
        {withMessage && (
          <label className="block space-y-1 text-[12px]">
            <span className="text-muted-foreground">Message (optional)</span>
            <textarea
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              rows={3}
              className="w-full resize-none rounded-md border border-border bg-background px-2 py-1.5 text-[12px] outline-none focus:border-accent"
            />
          </label>
        )}
        <div className="flex justify-end gap-2 pt-1">
          <Button type="button" size="sm" onClick={onCancel}>
            Cancel
          </Button>
          <Button type="submit" size="sm" variant="primary" disabled={!name.trim() || busy}>
            {confirmLabel}
          </Button>
        </div>
      </form>
    </div>
  );
}
