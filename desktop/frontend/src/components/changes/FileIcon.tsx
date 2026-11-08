import { Box, Boxes, File, FileText, Film, Image, Music, Sheet, Type } from "lucide-react";
import { cn } from "@/lib/utils";

const icons = { text: FileText, image: Image, model: Box, audio: Music, video: Film, font: Type, table: Sheet, scene: Boxes };

export function FileIcon({ kind, className }: { kind: string; className?: string }) {
  const Icon = icons[kind as keyof typeof icons] ?? File;
  return <Icon className={cn("size-3.5 shrink-0 text-muted-foreground", className)} aria-hidden />;
}

const statuses: Record<string, { letter: string; label: string; className: string }> = {
  added: { letter: "A", label: "Added", className: "text-success" },
  untracked: { letter: "U", label: "New, not tracked yet", className: "text-success" },
  modified: { letter: "M", label: "Modified", className: "text-warning" },
  deleted: { letter: "D", label: "Deleted", className: "text-danger" },
  renamed: { letter: "R", label: "Renamed", className: "text-accent" },
};

export function StatusLetter({ status }: { status: string }) {
  const s = statuses[status] ?? { letter: "?", label: status, className: "text-muted-foreground" };
  return (
    <span title={s.label} aria-label={s.label} className={cn("w-3 shrink-0 text-center font-mono text-[11px] font-semibold", s.className)}>
      {s.letter}
    </span>
  );
}
