import { FileWarning } from "lucide-react";
import type { FileDiff, FileSide } from "@/api/desktop";
import { FileIcon } from "@/components/changes/FileIcon";
import { formatBytes } from "@/lib/diff";

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

export function Notice({ diff }: { diff: FileDiff }) {
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
  scene: "Scene",
  opaque: "Binary",
};

export function MetaDiff({ diff, detail }: { diff: FileDiff; detail?: string }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 p-8 text-center">
      <FileIcon kind={diff.kind} className="size-8" />
      <p className="text-sm font-medium">{kindNames[diff.kind] ?? "Binary"} file</p>
      <p className="max-w-sm text-xs text-muted-foreground">{detail ?? "A visual compare for this file type is coming in a later version."}</p>
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
