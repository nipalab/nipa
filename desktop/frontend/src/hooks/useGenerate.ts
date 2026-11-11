import { useCallback, useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { desktop, type Draft } from "@/api/desktop";
import { withGeneratedTrailer } from "@/lib/generate";
import { errorMessage } from "@/lib/utils";

export const llmSettingsKey = ["llm-settings"] as const;

export function useLLMSettings() {
  return useQuery({ queryKey: llmSettingsKey, queryFn: desktop.llmSettings, staleTime: 30_000 });
}

// useDraft tracks the model behind generated text placed in a field: the
// trailer is added while the field holds that text (edited or not) and
// forgotten once the field is cleared.
export function useDraft(text: string, fill: (draft: Draft) => void) {
  const [model, setModel] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (model && text.trim() === "") setModel(null);
  }, [text, model]);

  const run = useCallback(
    async (generate: () => Promise<Draft>) => {
      setBusy(true);
      setError("");
      try {
        const draft = await generate();
        fill(draft);
        setModel(draft.model || null);
      } catch (err) {
        setError(errorMessage(err));
      } finally {
        setBusy(false);
      }
    },
    [fill],
  );

  return { model, busy, error, run, finalize: (value: string) => withGeneratedTrailer(value, model) };
}
