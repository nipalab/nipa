import { useEffect, useRef } from "react";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { shortcutGroups, shortcutKeys, shortcuts } from "@/lib/shortcuts";

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  const close = useRef<HTMLButtonElement>(null);
  useEffect(() => close.current?.focus(), []);
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Escape" || e.key === "?") {
          e.preventDefault();
          onClose();
        }
      }}
    >
      <div role="dialog" aria-modal="true" aria-labelledby="shortcuts-title" className="flex max-h-[80vh] w-full max-w-lg flex-col rounded-lg border border-border bg-card shadow-2xl">
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <h2 id="shortcuts-title" className="text-sm font-semibold">
            Keyboard shortcuts
          </h2>
          <Button ref={close} variant="ghost" size="icon" onClick={onClose} aria-label="Close">
            <X />
          </Button>
        </div>
        <div className="min-h-0 space-y-4 overflow-y-auto p-4">
          {shortcutGroups.map((group) => (
            <section key={group} aria-label={group}>
              <h3 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{group}</h3>
              <dl className="space-y-1 text-xs">
                {shortcuts
                  .filter((s) => s.group === group)
                  .map((s) => (
                    <div key={s.id} className="flex items-center justify-between gap-4">
                      <dt>{s.label}</dt>
                      <dd className="flex shrink-0 items-center gap-1.5 text-muted-foreground">
                        {shortcutKeys(s).map((keys, i) => (
                          <span key={i} className="flex items-center gap-0.5">
                            {i > 0 && <span className="mr-1">or</span>}
                            {keys.map((k) => (
                              <kbd key={k} className="min-w-5 rounded border border-border bg-muted px-1 text-center font-sans text-[10px] text-foreground">
                                {k}
                              </kbd>
                            ))}
                          </span>
                        ))}
                      </dd>
                    </div>
                  ))}
              </dl>
            </section>
          ))}
        </div>
      </div>
    </div>
  );
}
