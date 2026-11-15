import { Brush, Wrench } from "lucide-react";
import { Dialog } from "@/components/ui/dialog";
import { setMode, type Mode } from "@/lib/mode";
import { useTranslation } from "react-i18next";
import { translate } from "@/i18n";

const choices: { mode: Mode; title: string; detail: string; Icon: typeof Brush }[] = [
  { mode: "simple", title: translate("modePrompt.iMostlyWorkOnArt"), detail: translate("modePrompt.fewerTabsAndPlainWords"), Icon: Brush },
  { mode: "full", title: translate("modePrompt.iWorkWithCodeOr"), detail: translate("modePrompt.everythingBranchesTagsMergeRequests"), Icon: Wrench },
];

export function ModePrompt() {
  const { t } = useTranslation();
  return (
    <Dialog title={t("modePrompt.howDoYouUseNipa")} onClose={() => setMode("full")}>
      <p className="text-xs text-muted-foreground">{t("modePrompt.youCanChangeThisAny")}</p>
      <div className="space-y-2">
        {choices.map(({ mode, title, detail, Icon }) => (
          <button key={mode} type="button" onClick={() => setMode(mode)} className="flex w-full items-start gap-3 rounded-md border border-border p-3 text-left hover:bg-muted">
            <Icon className="mt-0.5 size-4 shrink-0 text-accent" />
            <span className="space-y-0.5">
              <span className="block text-[13px] font-medium">{title}</span>
              <span className="block text-xs text-muted-foreground">{detail}</span>
            </span>
          </button>
        ))}
      </div>
    </Dialog>
  );
}
