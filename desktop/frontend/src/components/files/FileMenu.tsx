import { useEffect, useRef } from "react";
import type { LucideIcon } from "lucide-react";
import { Button } from "@/components/ui/button";

export type FileAction = { id: string; label: string; Icon: LucideIcon; run: () => void; disabled?: boolean; title?: string };

export function ActionBar({ actions }: { actions: FileAction[] }) {
  return (
    <div className="flex flex-wrap gap-1" role="toolbar" aria-label="File actions">
      {actions.map((a) => (
        <Button key={a.id} size="sm" variant="ghost" onClick={a.run} disabled={a.disabled} title={a.title ?? a.label}>
          <a.Icon />
          {a.label}
        </Button>
      ))}
    </div>
  );
}

// FileMenu is the right-click menu of a file or folder, placed at the pointer.
export function FileMenu({ x, y, actions, onClose }: { x: number; y: number; actions: FileAction[]; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus();
    const away = (e: Event) => {
      if (!ref.current?.contains(e.target as Node)) onClose();
    };
    const key = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", key);
    };
  }, [onClose]);
  return (
    <div
      ref={ref}
      role="menu"
      className="fixed z-50 min-w-48 rounded-md border border-border bg-card py-1 text-[12px] shadow-xl"
      style={{ left: Math.min(x, window.innerWidth - 200), top: Math.min(y, window.innerHeight - actions.length * 28 - 8) }}
    >
      {actions.map((a) => (
        <button
          key={a.id}
          type="button"
          role="menuitem"
          disabled={a.disabled}
          title={a.title}
          onClick={() => {
            onClose();
            a.run();
          }}
          className="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-muted focus-visible:bg-muted focus-visible:outline-none disabled:opacity-50"
        >
          <a.Icon className="size-3.5 text-muted-foreground" />
          {a.label}
        </button>
      ))}
    </div>
  );
}
