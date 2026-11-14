import { useEffect, useId, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Save } from "lucide-react";
import { desktop, type IgnoreRules } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { errorMessage } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { translate } from "@/i18n";

type IgnoreRulesEditorProps = {
  root: string;
  notify: (message: string, tone?: "error" | "success") => void;
};

export function IgnoreRulesEditor({ root, notify }: IgnoreRulesEditorProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const rules = useQuery({
    queryKey: ["ignore", root],
    queryFn: () => desktop.ignoreRules(root),
  });
  const [draft, setDraft] = useState<IgnoreRules | null>(null);
  const ids = { shared: useId(), local: useId() };
  useEffect(() => {
    if (rules.data) setDraft(rules.data);
  }, [rules.data]);

  const save = useMutation({
    mutationFn: (next: IgnoreRules) => desktop.saveIgnoreRules(root, next),
    onSuccess: (_, next) => {
      queryClient.setQueryData(["ignore", root], next);
      void queryClient.invalidateQueries({ queryKey: ["changes", root] });
      notify(translate("ignoreRulesEditor.ignoreRulesSaved"), "success");
    },
    onError: (err) => notify(translate("ignoreRulesEditor.couldNotSaveTheIgnore", { value: errorMessage(err) })),
  });

  if (rules.isError) return <p className="text-danger">{t("ignoreRulesEditor.readFailed", { error: errorMessage(rules.error) })}</p>;
  if (!draft) return <p className="text-muted-foreground">{t("ignoreRulesEditor.loadingIgnoreRules")}</p>;
  const dirty = draft.shared !== rules.data?.shared || draft.local !== rules.data?.local;
  const area = "h-28 w-full resize-y rounded-md border border-border bg-background px-2 py-1.5 font-mono text-[11px] outline-none focus:border-accent";
  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <label htmlFor={ids.shared} className="font-medium">
          {t("ignoreRulesEditor.sharedRulesNipaignore")}
        </label>
        <p className="text-muted-foreground">{t("ignoreRulesEditor.versionedWithTheProjectSaving")}</p>
        <textarea id={ids.shared} value={draft.shared} onChange={(e) => setDraft({ ...draft, shared: e.target.value })} placeholder={"*.tmp\nBuild/\nLibrary/"} className={area} />
      </div>
      <div className="space-y-1">
        <label htmlFor={ids.local} className="font-medium">
          {t("ignoreRulesEditor.onlyOnThisComputerNipaIgnore")}
        </label>
        <textarea id={ids.local} value={draft.local} onChange={(e) => setDraft({ ...draft, local: e.target.value })} placeholder={t("ignoreRulesEditor.scratch")} className={area} />
      </div>
      <Button size="sm" onClick={() => save.mutate(draft)} disabled={!dirty || save.isPending}>
        <Save />
        {t("ignoreRulesEditor.saveIgnoreRules")}
      </Button>
    </div>
  );
}
