import type { ReactNode } from "react";
import { FileCheck2 } from "lucide-react";
import type { ConflictFile, ConflictSet, ResolveChoice } from "@/api/desktop";
import { FileIcon } from "@/components/changes/FileIcon";
import { Button } from "@/components/ui/button";
import { choices, kindLabel } from "@/lib/conflicts";

type HeaderProps = { file: ConflictFile; set: ConflictSet; busy: boolean; onResolve: (choice: ResolveChoice, content?: string) => void; children?: ReactNode };

export function ResolverHeader({ file, set, busy, onResolve, children }: HeaderProps) {
  const [mine, theirs] = choices(file, set);
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-4 py-2 text-[12px]">
      <FileIcon kind={file.viewer} />
      <span className="min-w-0 truncate font-medium" title={file.path}>
        {file.path}
      </span>
      <span className="text-[11px] text-muted-foreground">{kindLabel(file.kind)}</span>
      {file.resolved && (
        <span className="flex items-center gap-1 text-[11px] text-success">
          <FileCheck2 className="size-3" /> Resolved
        </span>
      )}
      <span className="ml-auto flex flex-wrap gap-2">
        {children}
        <Button size="sm" disabled={busy} title={mine.hint} onClick={() => onResolve(mine.choice)}>
          {mine.label}
        </Button>
        <Button size="sm" disabled={busy} title={theirs.hint} onClick={() => onResolve(theirs.choice)}>
          {theirs.label}
        </Button>
      </span>
    </div>
  );
}

export function Mono({ children }: { children: ReactNode }) {
  return <span className="font-mono">{children}</span>;
}

export function Centered({ children }: { children: ReactNode }) {
  return <div className="flex h-full flex-1 items-center justify-center gap-2 p-6 text-center text-xs text-muted-foreground">{children}</div>;
}
