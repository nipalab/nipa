import { AlertTriangle, Loader2, UserRound } from "lucide-react";
import type { TextTools } from "@/hooks/useTextTools";
import { cn, errorMessage } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { translate } from "@/i18n";

export function BlameToggle({ tools }: { tools: TextTools }) {
  const { t } = useTranslation();
  if (!tools.available) return null;
  const title = tools.error ? translate("blameToggle.blameIsNotAvailable", { value: errorMessage(tools.error) }) : tools.on ? translate("blameToggle.hideWhoLastChangedEach") : translate("blameToggle.showWhoLastChangedEach");
  return (
    <button
      type="button"
      aria-pressed={tools.on}
      title={title}
      onClick={tools.toggle}
      className={cn("flex h-6 shrink-0 items-center gap-1 rounded-md border border-border px-1.5 text-[11px] text-muted-foreground hover:text-foreground", tools.on && "bg-muted text-foreground")}
    >
      {tools.loading ? <Loader2 className="size-3 animate-spin" /> : tools.error ? <AlertTriangle className="size-3 text-danger" /> : <UserRound className="size-3" />}
      {t("blameToggle.blame")}
    </button>
  );
}
