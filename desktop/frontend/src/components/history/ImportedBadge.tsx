import { useState } from "react";
import { Check, Copy, Import } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { originShortId, originSystemName } from "@/lib/origin";

export function ImportedBadge({ origin, copy = false }: { origin?: string; copy?: boolean }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const system = originSystemName(origin);
  if (!origin || !system) return null;
  const short = originShortId(origin);
  const label = t("history.importedFrom", { system });
  const onCopy = () => {
    navigator.clipboard?.writeText(origin).then(
      () => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      },
      () => setCopied(false),
    );
  };
  return (
    <span className="inline-flex shrink-0 items-center gap-1" title={`${label}: ${origin}`}>
      <span className="flex items-center gap-0.5 rounded bg-muted px-1 text-[10px] text-muted-foreground">
        <Import className="size-2.5" />
        {copy ? label : system}
      </span>
      <span className="select-text font-mono">{short}</span>
      {copy && (
        <Button variant="ghost" size="icon" className="size-5 [&_svg]:size-3" onClick={onCopy} aria-label={copied ? t("history.originCopied") : t("history.copyOrigin", { system })}>
          {copied ? <Check /> : <Copy />}
        </Button>
      )}
    </span>
  );
}
