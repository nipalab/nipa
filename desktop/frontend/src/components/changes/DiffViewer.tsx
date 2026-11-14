import { lazy, Suspense, type ComponentType } from "react";
import { ArrowRight, Columns2, Rows3 } from "lucide-react";
import type { FileDiff } from "@/api/desktop";
import { AudioDiff } from "@/components/changes/AudioDiff";
import { BlameToggle } from "@/components/changes/BlameToggle";
import { FileIcon, StatusLetter } from "@/components/changes/FileIcon";
import { ImageDiff } from "@/components/changes/ImageDiff";
import { MetaDiff, Notice } from "@/components/changes/Notices";
import { SceneDiff } from "@/components/changes/SceneDiff";
import { TableDiff } from "@/components/changes/TableDiff";
import { TextDiff, type DiffLayout, type LineComments, type LineTools } from "@/components/changes/TextDiff";
import { useTextTools } from "@/hooks/useTextTools";
import { cn } from "@/lib/utils";

const ModelDiff = lazy(() => import("@/components/changes/ModelDiff").then((m) => ({ default: m.ModelDiff })));

type ViewerProps = { root: string; diff: FileDiff; layout: DiffLayout; comments?: LineComments; tools?: LineTools };

const isText = (d: FileDiff) => !d.old?.binary && !d.new?.binary;

// viewerFor picks the viewer for a change: visual viewers for images, models,
// audio, tables and scenes, the text diff, or metadata for anything else.
export function viewerFor(diff: FileDiff): ComponentType<ViewerProps> {
  if (diff.contentUnavailable) return Notice;
  if (diff.kind === "image" && (diff.old || diff.new)) return ImageDiff;
  if (diff.kind === "model" && (diff.old || diff.new)) return ModelDiff;
  if (diff.kind === "audio" && (diff.old || diff.new)) return AudioDiff;
  if (diff.kind === "table" && (diff.old?.size || diff.new?.size)) return TableDiff;
  if (diff.kind === "scene" && (diff.old?.size || diff.new?.size)) return SceneDiff;
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
  comments?: LineComments;
};

export function DiffViewer({ root, diff, layout, onLayout, comments }: DiffViewerProps) {
  const Viewer = viewerFor(diff);
  const tools = useTextTools(root, diff, Viewer === TextDiff);
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
        <BlameToggle tools={tools} />
        {(Viewer === TextDiff || ((Viewer === TableDiff || Viewer === SceneDiff) && (diff.hunks ?? []).length > 0)) && (
          <div className="ml-1 flex rounded-md border border-border p-0.5" role="group" aria-label="Diff layout">
            <LayoutButton active={layout === "unified"} label="Unified" onClick={() => onLayout("unified")} Icon={Rows3} />
            <LayoutButton active={layout === "split"} label="Split" onClick={() => onLayout("split")} Icon={Columns2} />
          </div>
        )}
      </div>
      <div className="min-h-0 flex-1">
        <Suspense fallback={null}>
          <Viewer root={root} diff={diff} layout={layout} comments={comments} tools={tools} />
        </Suspense>
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
