import { useId, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2, PlugZap, X } from "lucide-react";
import { desktop } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { llmSettingsKey } from "@/hooks/useGenerate";
import { errorMessage } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { translate } from "@/i18n";

export type AIForm = { endpoint: string; model: string; key: string };

export function aiFormValid(f: AIForm): boolean {
  return (f.endpoint.trim() === "") === (f.model.trim() === "");
}

type AISectionProps = {
  value: AIForm;
  onChange: (patch: Partial<AIForm>) => void;
  keySaved: boolean;
  configured: boolean;
  notify: (message: string, tone?: "error" | "success") => void;
};

export function AISection({ value, onChange, keySaved, configured, notify }: AISectionProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const ids = { endpoint: useId(), model: useId(), key: useId() };
  const [saved, setSaved] = useState(keySaved);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);
  const filled = value.endpoint.trim() !== "" && value.model.trim() !== "";

  const test = useMutation({
    mutationFn: () => desktop.testLLM(value.endpoint.trim(), value.model.trim(), value.key),
    onMutate: () => setResult(null),
    onSuccess: () => setResult({ ok: true, text: translate("aISection.answered", { trim: value.model.trim() }) }),
    onError: (err) => setResult({ ok: false, text: errorMessage(err) }),
  });

  const clear = useMutation({
    mutationFn: desktop.clearLLMKey,
    onSuccess: () => {
      setSaved(false);
      void queryClient.invalidateQueries({ queryKey: llmSettingsKey });
      notify(translate("aISection.aPIKeyRemoved"), "success");
    },
    onError: (err) => notify(translate("aISection.couldNotRemoveTheAPI", { value: errorMessage(err) })),
  });

  const input = "min-w-0 flex-1 rounded-md border border-border bg-background px-2 py-1 font-mono outline-none focus:border-accent";
  return (
    <>
      <p className="text-muted-foreground">{t("aISection.draftPushMessagesAndMerge")}</p>
      {!configured && <p className="text-muted-foreground">{t("aISection.setAnEndpointAndA")}</p>}
      <div className="flex items-center gap-3">
        <label htmlFor={ids.endpoint} className="w-20 shrink-0">
          {t("aISection.endpoint")}
        </label>
        <input id={ids.endpoint} value={value.endpoint} onChange={(e) => onChange({ endpoint: e.target.value })} placeholder={t("aISection.httpLocalhost11434V1")} className={input} />
      </div>
      <div className="flex items-center gap-3">
        <label htmlFor={ids.model} className="w-20 shrink-0">
          {t("aISection.model")}
        </label>
        <input id={ids.model} value={value.model} onChange={(e) => onChange({ model: e.target.value })} placeholder={t("aISection.qwen25Coder14b")} className={input} />
      </div>
      <div className="flex items-center gap-3">
        <label htmlFor={ids.key} className="w-20 shrink-0">
          {t("aISection.aPIKey")}
        </label>
        <input
          id={ids.key}
          type="password"
          autoComplete="off"
          value={value.key}
          onChange={(e) => onChange({ key: e.target.value })}
          placeholder={saved ? translate("aISection.saved") : translate("aISection.optional")}
          className={input}
        />
        {saved && (
          <Button type="button" size="sm" variant="ghost" onClick={() => clear.mutate()} disabled={clear.isPending}>
            <X />
            {t("aISection.clear")}
          </Button>
        )}
      </div>
      <p className="text-muted-foreground">{saved ? translate("aISection.aKeyIsSavedIn") : translate("aISection.theKeyIsKeptIn")}</p>
      {!aiFormValid(value) && <p className="text-danger">{t("aISection.setBothTheEndpointAnd")}</p>}
      <div className="flex items-center gap-2">
        <Button type="button" size="sm" onClick={() => test.mutate()} disabled={!filled || test.isPending}>
          {test.isPending ? <Loader2 className="animate-spin" /> : <PlugZap />}
          {t("aiSection.test")}
        </Button>
        {result && (
          <span role="status" className={result.ok ? "text-success" : "text-danger"}>
            {result.text}
          </span>
        )}
      </div>
    </>
  );
}
