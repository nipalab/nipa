import type { ReactNode } from "react";

export function Dialog({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  return (
    <div role="presentation" className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4" onKeyDown={(e) => e.key === "Escape" && onClose()}>
      <div role="dialog" aria-modal="true" aria-labelledby="dialog-title" className="w-full max-w-sm space-y-3 rounded-lg border border-border bg-card p-5 shadow-xl">
        <h2 id="dialog-title" className="text-sm font-semibold">
          {title}
        </h2>
        {children}
      </div>
    </div>
  );
}
