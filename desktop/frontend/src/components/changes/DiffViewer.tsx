import type { ComponentType } from "react";
import { ArrowRight, Columns2, FileWarning, Rows3 } from "lucide-react";
import type { FileDiff, FileSide } from "@/api/desktop";
import { FileIcon, StatusLetter } from "@/components/changes/FileIcon";
import { TextDiff, type DiffLayout } from "@/components/changes/TextDiff";
import { formatBytes } from "@/lib/diff";
import { cn } from "@/lib/utils";

type ViewerProps = { diff: FileDiff; layout: DiffLayout };

const isText = (d: FileDiff) => !d.old?.binary && !d.new?.binary;

// viewerFor picks the viewer for a change. Visual viewers for images, models
// and audio register here as they land; anything without one gets metadata.
export function viewerFor(diff: FileDiff): ComponentType<ViewerProps> {
  if (diff.hunksOmitted || diff.contentUnavailable) return Notice;
  if (isText(diff) && (diff.hunks ?? []).length > 0) return TextDiff;
  if (isText(diff)) return Notice;
  return MetaDiff;
}

type DiffViewerProps = {
  diff: FileDiff;
  layout: DiffLayout;
  onLayout: (layout: DiffLayout) => void;
};

export function DiffViewer({ diff, layout, onLayout }: DiffViewerProps) {
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
        <Viewer diff={diff} layout={layout} />
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

function noticeText(diff: FileDiff): { title: string; detail: string } {
  if (diff.contentUnavailable) return { title: "Content not available", detail: "The old version of this file is not in the local cache." };
  if (diff.hunksOmitted) return { title: "This diff is too large to show", detail: "Open the file in your editor to review the change." };
  if (diff.status === "deleted") return { title: "File deleted", detail: "The file was empty." };
  if (diff.status === "renamed") return { title: "File renamed", detail: "The content did not change." };
  if (diff.old && diff.new && diff.old.mode !== diff.new.mode) {
    return { title: "File mode changed", detail: `${diff.old.mode.toString(8)} → ${diff.new.mode.toString(8)}` };
  }
  return { title: "Empty file", detail: "There is no content to compare." };
}

function Notice({ diff }: ViewerProps) {
  const { title, detail } = noticeText(diff);
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center">
      <FileWarning className="size-6 text-muted-foreground" />
      <p className="text-sm font-medium">{title}</p>
      <p className="max-w-sm text-xs text-muted-foreground">{detail}</p>
      <Sizes diff={diff} />
    </div>
  );
}

const kindNames: Record<string, string> = {
  image: "Image",
  model: "3D model",
  audio: "Audio",
  video: "Video",
  font: "Font",
  table: "Table",
  opaque: "Binary",
};

function MetaDiff({ diff }: ViewerProps) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 p-8 text-center">
      <FileIcon kind={diff.kind} className="size-8" />
      <p className="text-sm font-medium">{kindNames[diff.kind] ?? "Binary"} file</p>
      <p className="max-w-sm text-xs text-muted-foreground">A visual compare for this file type is coming in a later version.</p>
      <Sizes diff={diff} />
    </div>
  );
}

function Sizes({ diff }: { diff: FileDiff }) {
  if (!diff.old && !diff.new) return null;
  return (
    <dl className="mt-2 grid grid-cols-[auto_auto] gap-x-6 gap-y-1 rounded-lg border border-border bg-card px-4 py-3 text-left text-xs">
      <Side label="Before" side={diff.old} />
      <Side label="After" side={diff.new} />
    </dl>
  );
}

function Side({ label, side }: { label: string; side: FileSide | null }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="font-mono" title={side?.hash || undefined}>
        {side ? `${formatBytes(side.size)}${side.hash ? ` · ${side.hash.slice(0, 10)}` : ""}` : "—"}
      </dd>
    </>
  );
}
