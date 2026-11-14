import { FileWarning } from "lucide-react";
import type { FileDiff, FileSide } from "@/api/desktop";
import { FileIcon } from "@/components/changes/FileIcon";
import { formatBytes } from "@/lib/diff";
import { useTranslation } from "react-i18next";
import { translate, translate as t } from "@/i18n";

function noticeText(diff: FileDiff): { title: string; detail: string } {
  if (diff.contentUnavailable) return { title: t("notices.contentUnavailable"), detail: t("notices.contentUnavailableDetail") };
  if (diff.hunksOmitted) return { title: t("notices.tooLarge"), detail: t("notices.tooLargeDetail") };
  if (diff.status === "deleted") return { title: t("notices.deleted"), detail: t("notices.deletedDetail") };
  if (diff.status === "renamed") return { title: t("notices.renamed"), detail: t("notices.renamedDetail") };
  if (diff.old && diff.new && diff.old.mode !== diff.new.mode) {
    return { title: t("notices.modeChanged"), detail: translate("notices.text", { value: diff.old.mode.toString(8), value2: diff.new.mode.toString(8) }) };
  }
  return { title: t("notices.empty"), detail: t("notices.emptyDetail") };
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

const kindNames = (): Record<string, string> => ({
  image: t("notices.kind.image"),
  model: t("notices.kind.model"),
  audio: t("notices.kind.audio"),
  video: t("notices.kind.video"),
  font: t("notices.kind.font"),
  table: t("notices.kind.table"),
  scene: t("notices.kind.scene"),
  opaque: t("notices.kind.opaque"),
});

export function MetaDiff({ diff, detail }: { diff: FileDiff; detail?: string }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 p-8 text-center">
      <FileIcon kind={diff.kind} className="size-8" />
      <p className="text-sm font-medium">{t("notices.fileOfKind", { kind: kindNames()[diff.kind] ?? t("notices.kind.opaque") })}</p>
      <p className="max-w-sm text-xs text-muted-foreground">{detail ?? t("notices.comingLater")}</p>
      <Sizes diff={diff} />
    </div>
  );
}

function Sizes({ diff }: { diff: FileDiff }) {
  const { t } = useTranslation();
  if (!diff.old && !diff.new) return null;
  return (
    <dl className="mt-2 grid grid-cols-[auto_auto] gap-x-6 gap-y-1 rounded-lg border border-border bg-card px-4 py-3 text-left text-xs">
      <Side label={t("notices.before")} side={diff.old} />
      <Side label={t("notices.after")} side={diff.new} />
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
