import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FileCog, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { desktop } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { usePersistedState } from "@/hooks/usePersistedState";
import { repoSetting } from "@/lib/settings";
import type { Notify } from "@/hooks/useLocks";

const isBoolean = (v: unknown): v is boolean => typeof v === "boolean";

// GitignoreOffer offers, once per clone, to copy a root .gitignore into
// .nipaignore, since Nipa never reads .gitignore.
export function GitignoreOffer({ root, notify }: { root: string; notify: Notify }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [dismissed, setDismissed] = usePersistedState(repoSetting(root, "changes.gitignoreOffer"), false, { valid: isBoolean });
  const offer = useQuery({
    queryKey: ["gitignore-offer", root],
    queryFn: () => desktop.canImportGitignore(root),
    enabled: !dismissed,
  });
  const importRules = useMutation({
    mutationFn: () => desktop.importGitignore(root),
    onSuccess: (r) => {
      setDismissed(true);
      void queryClient.invalidateQueries({ queryKey: ["gitignore-offer", root] });
      notify(t("gitignoreOffer.imported", { count: r.added }), "success");
      if (r.unsupported.length > 0) notify(t("gitignoreOffer.unsupported", { count: r.unsupported.length, rules: r.unsupported.join(", ") }), "error");
    },
    onError: (err) => notify(err instanceof Error ? err.message : String(err), "error"),
  });

  if (dismissed || !offer.data) return null;
  return (
    <div className="flex items-start gap-2 border-b border-border bg-muted px-3 py-2 text-[11px]">
      <FileCog className="mt-px size-3.5 shrink-0 text-accent" />
      <div className="min-w-0 flex-1 space-y-1.5">
        <p>{t("gitignoreOffer.text")}</p>
        <Button size="sm" variant="primary" disabled={importRules.isPending} onClick={() => importRules.mutate()}>
          {t("gitignoreOffer.create")}
        </Button>
      </div>
      <button type="button" className="text-muted-foreground hover:text-foreground" aria-label={t("gitignoreOffer.dismiss")} onClick={() => setDismissed(true)}>
        <X className="size-3.5" />
      </button>
    </div>
  );
}
