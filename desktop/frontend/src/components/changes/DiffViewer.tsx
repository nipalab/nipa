import type { ComponentType } from "react";
import { ArrowRight, Columns2, Rows3 } from "lucide-react";
import type { FileDiff } from "@/api/desktop";
import { FileIcon, StatusLetter } from "@/components/changes/FileIcon";
import { ImageDiff } from "@/components/changes/ImageDiff";
import { MetaDiff, Notice } from "@/components/changes/Notices";
import { TextDiff, type DiffLayout } from "@/components/changes/TextDiff";
import { cn } from "@/lib/utils";

type ViewerProps = { root: string; diff: FileDiff; layout: DiffLayout };

const isText = (d: FileDiff) => !d.old?.binary && !d.new?.binary;

// viewerFor picks the viewer for a change. Visual viewers for images, models
// and audio register here as they land; anything without one gets metadata.
export function viewerFor(diff: FileDiff): ComponentType<ViewerProps> {
  if (diff.contentUnavailable) return Notice;
  if (diff.kind === "image" && (diff.old || diff.new)) return ImageDiff;
  if (diff.hunksOmitted) return Notice;
  if (isText(diff) && (diff.hunks ?? []).length > 0) return TextDiff;
  if (isText(diff)) return Notice;
  return MetaDiff;
}

type DiffViewerProps = {
  root: string;
  diff: FileDiff;
  layout: DiffLayout;
  onLayout: (layout: DiffLayout) => void;
};

export function DiffViewer({ root, diff, layout, onLayout }: DiffViewerProps) {
  const Viewer = viewerFor(diff);
  return (
    <div className="flex h-full min-w-0 flex-col">
      <div className="flex h-10 shrink-0 items-center gap-2 border-b border-border px-4">
        <FileIcon kind={diff.kind} />
        <span className="min-w-0 flex-1 truncate text-[12px] font-medium" title={diff.path}>
          {diff.oldPath ? (
            <>
              <span className="text-muted-foreground">{diff.oldPath}</span>
              <ArrowRight className="mx-1 inline size-3 text-muted-foreground" />
            </>
          ) : null}
          {diff.path}
        </span>
        {diff.similarity ? <span className="text-[11px] text-muted-foreground">{diff.similarity}% similar</span> : null}
        {(diff.additions > 0 || diff.deletions > 0) && (
          <span className="font-mono text-[11px]">
            <span className="text-success">+{diff.additions}</span> <span className="text-danger">−{diff.deletions}</span>
          </span>
        )}
        <StatusLetter status={diff.untracked ? "untracked" : diff.status} />
        {Viewer === TextDiff && (
          <div className="ml-1 flex rounded-md border border-border p-0.5" role="group" aria-label="Diff layout">
            <LayoutButton active={layout === "unified"} label="Unified" onClick={() => onLayout("unified")} Icon={Rows3} />
            <LayoutButton active={layout === "split"} label="Split" onClick={() => onLayout("split")} Icon={Columns2} />
          </div>
        )}
      </div>
      <div className="min-h-0 flex-1">
        <Viewer root={root} diff={diff} layout={layout} />
      </div>
    </div>
  );
}

function LayoutButton({ active, label, onClick, Icon }: { active: boolean; label: string; onClick: () => void; Icon: typeof Rows3 }) {
  return (
    <button
      type="button"
      aria-pressed={active}
      title={label}
      onClick={onClick}
      className={cn("flex size-6 items-center justify-center rounded text-muted-foreground hover:text-foreground", active && "bg-muted text-foreground")}
    >
      <Icon className="size-3.5" />
      <span className="sr-only">{label}</span>
    </button>
  );
}
