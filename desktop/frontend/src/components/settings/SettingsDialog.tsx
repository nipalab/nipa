import { useEffect, useId, useState, type FormEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FolderOpen, Plus, RotateCw, X } from "lucide-react";
import { desktop, type LLMSettings, type Settings, type Tool } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { ThemeToggle } from "@/components/ThemeToggle";
import { UpdatesSection } from "@/components/settings/UpdatesSection";
import { AISection, aiFormValid, type AIForm } from "@/components/settings/AISection";
import { llmSettingsKey, useLLMSettings } from "@/hooks/useGenerate";
import { setMode, startTour, useMode } from "@/lib/mode";
import { errorMessage } from "@/lib/utils";
import { Trans, useTranslation } from "react-i18next";
import { changeLanguage, LANGUAGE_NAMES, LANGUAGES, storedLanguage, translate, type LanguageSetting } from "@/i18n";

export const pollOptions: { seconds: number; label: string }[] = [
  { seconds: 30, label: translate("settingsDialog.30Seconds") },
  { seconds: 60, label: translate("settingsDialog.1Minute") },
  { seconds: 300, label: translate("settingsDialog.5Minutes") },
  { seconds: 900, label: translate("settingsDialog.15Minutes") },
  { seconds: 3600, label: translate("settingsDialog.1Hour") },
];

type SettingsDialogProps = {
  canReplayTour?: boolean;
  onClose: () => void;
  notify: (message: string, tone?: "error" | "success") => void;
};

export function SettingsDialog({ onClose, notify, canReplayTour = true }: SettingsDialogProps) {
  const { t } = useTranslation();
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);
  const settings = useQuery({
    queryKey: ["settings"],
    queryFn: desktop.settings,
  });
  const workers = useQuery({
    queryKey: ["upload-workers"],
    queryFn: desktop.uploadWorkers,
  });
  const llm = useLLMSettings();
  return (
    <div role="presentation" className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4">
      <div role="dialog" aria-modal="true" aria-labelledby="settings-title" className="flex max-h-full w-full max-w-lg flex-col rounded-lg border border-border bg-card shadow-xl">
        <div className="flex items-center justify-between border-b border-border px-5 py-3">
          <h2 id="settings-title" className="text-sm font-semibold">
            {t("settingsDialog.settings")}
          </h2>
          <Button variant="ghost" size="icon" onClick={onClose} aria-label={t("settingsDialog.closeSettings")}>
            <X />
          </Button>
        </div>
        {settings.isError || workers.isError || llm.isError ? (
          <p role="alert" className="p-5 text-xs text-danger">
            {t("settingsDialog.loadFailed", { error: errorMessage(settings.error ?? workers.error ?? llm.error) })}
          </p>
        ) : settings.data && workers.data !== undefined && llm.data ? (
          <SettingsForm initial={settings.data} initialWorkers={workers.data} initialLLM={llm.data} onClose={onClose} notify={notify} canReplayTour={canReplayTour} />
        ) : (
          <p role="status" className="p-5 text-xs text-muted-foreground">
            {t("settingsDialog.loadingSettings")}
          </p>
        )}
      </div>
    </div>
  );
}

type FormProps = {
  initial: Settings;
  initialWorkers: number;
  initialLLM: LLMSettings;
  onClose: () => void;
  notify: SettingsDialogProps["notify"];
  canReplayTour: boolean;
};

function SettingsForm({ initial, initialWorkers, initialLLM, onClose, notify, canReplayTour }: FormProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<Settings>(initial);
  const [workers, setWorkers] = useState(initialWorkers > 0 ? String(initialWorkers) : "");
  const [restartHint, setRestartHint] = useState(false);
  const [ai, setAI] = useState<AIForm>({ endpoint: initialLLM.endpoint, model: initialLLM.model, key: "" });
  const ids = { workers: useId(), poll: useId() };
  const set = (patch: Partial<Settings>) => setDraft((d) => ({ ...d, ...patch }));
  const setTool = (i: number, patch: Partial<Tool>) =>
    set({
      tools: draft.tools.map((t, j) => (j === i ? { ...t, ...patch } : t)),
    });
  const workerCount = workers.trim() === "" ? 0 : Number(workers);
  const workersValid = Number.isInteger(workerCount) && workerCount >= 0 && workerCount <= 64;
  const valid = workersValid && aiFormValid(ai);

  const save = useMutation({
    mutationFn: async () => {
      const saved = await desktop.saveSettings(draft);
      if (workerCount !== initialWorkers) await desktop.setUploadWorkers(workerCount);
      if (ai.endpoint.trim() !== initialLLM.endpoint || ai.model.trim() !== initialLLM.model) await desktop.setLLM(ai.endpoint.trim(), ai.model.trim());
      if (ai.key.trim() !== "") await desktop.setLLMKey(ai.key.trim());
      return saved;
    },
    onSettled: () => void queryClient.invalidateQueries({ queryKey: llmSettingsKey }),
    onSuccess: (saved) => {
      queryClient.setQueryData(["settings"], saved);
      queryClient.setQueryData(["upload-workers"], workerCount);
      if (workerCount !== initialWorkers) {
        setRestartHint(true);
        setDraft(saved);
        return;
      }
      notify(translate("settingsDialog.settingsSaved"), "success");
      onClose();
    },
  });

  const restart = useMutation({
    mutationFn: desktop.restartDaemon,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["daemon"] });
      void queryClient.invalidateQueries({ queryKey: ["repos"] });
      notify(translate("settingsDialog.backgroundServiceRestarted"), "success");
      onClose();
    },
    onError: (err) => notify(translate("settingsDialog.couldNotRestartTheBackground", { value: errorMessage(err) })),
  });

  const browse = async (i: number) => {
    try {
      const program = await desktop.chooseProgram();
      if (program) setTool(i, { command: program });
    } catch (err) {
      notify(errorMessage(err));
    }
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (valid) save.mutate();
  };

  return (
    <form onSubmit={submit} className="flex min-h-0 flex-col">
      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-4 text-[12px]">
        <Section title={t("settingsDialog.appearance")}>
          <div className="flex items-center justify-between">
            <span>{t("settingsDialog.theme")}</span>
            <ThemeToggle />
          </div>
        </Section>

        <Section title={t("settingsDialog.background")}>
          <Check label={t("settingsDialog.keepNipaRunningInThe")} checked={draft.keepInTray} onChange={(v) => set({ keepInTray: v })} />
          <Check label={t("settingsDialog.startNipaWhenILog")} checked={draft.startAtLogin} onChange={(v) => set({ startAtLogin: v })} />
        </Section>

        <Section title={t("settingsDialog.notifications")}>
          <Check label={t("settingsDialog.teammatesPushToMyBranch")} checked={draft.notify.pushes} onChange={(v) => set({ notify: { ...draft.notify, pushes: v } })} />
          <Check label={t("settingsDialog.aLockIsReleased")} checked={draft.notify.locks} onChange={(v) => set({ notify: { ...draft.notify, locks: v } })} />
          <Check label={t("settingsDialog.someoneAsksMeToRelease")} checked={draft.notify.lockRequests} onChange={(v) => set({ notify: { ...draft.notify, lockRequests: v } })} />
          <Check label={t("settingsDialog.someoneAsksForMyReview")} checked={draft.notify.reviews} onChange={(v) => set({ notify: { ...draft.notify, reviews: v } })} />
          <div className="flex items-center justify-between gap-3 pt-1">
            <label htmlFor={ids.poll}>{t("settingsDialog.checkTheServerEvery")}</label>
            <select
              id={ids.poll}
              value={draft.pollSeconds}
              onChange={(e) => set({ pollSeconds: Number(e.target.value) })}
              className="rounded-md border border-border bg-background px-2 py-1 outline-none focus:border-accent"
            >
              {pollChoices(draft.pollSeconds).map((o) => (
                <option key={o.seconds} value={o.seconds}>
                  {o.label}
                </option>
              ))}
            </select>
          </div>
        </Section>

        <Section title={t("settingsDialog.transfers")}>
          <div className="flex items-center justify-between gap-3">
            <label htmlFor={ids.workers}>{t("settingsDialog.uploadsAtOnce")}</label>
            <input
              id={ids.workers}
              inputMode="numeric"
              value={workers}
              onChange={(e) => setWorkers(e.target.value)}
              placeholder={t("settingsDialog.automatic")}
              aria-invalid={!workersValid}
              className="w-28 rounded-md border border-border bg-background px-2 py-1 text-right outline-none focus:border-accent aria-[invalid=true]:border-danger"
            />
          </div>
          <p className="text-muted-foreground">{t("settingsDialog.leaveEmptyToTuneIt")}</p>
          {!workersValid && <p className="text-danger">{t("settingsDialog.enterANumberFrom1")}</p>}
          {restartHint && (
            <div role="status" className="flex items-center justify-between gap-3 rounded-md bg-accent-soft px-3 py-2">
              <span>{t("settingsDialog.restartTheBackgroundServiceTo")}</span>
              <Button type="button" size="sm" onClick={() => restart.mutate()} disabled={restart.isPending}>
                <RotateCw />
                {t("settingsDialog.restart")}
              </Button>
            </div>
          )}
        </Section>

        <Section title={t("settingsDialog.interface")}>
          <InterfaceSection onTour={onClose} canReplay={canReplayTour} />
        </Section>

        <Section title={t("settingsDialog.aIAssistance")}>
          <AISection value={ai} onChange={(patch) => setAI((f) => ({ ...f, ...patch }))} keySaved={initialLLM.keySaved} configured={initialLLM.configured} notify={notify} />
        </Section>

        <Section title={t("settingsDialog.updates")}>
          <UpdatesSection auto={!draft.manualUpdates} onAuto={(v) => set({ manualUpdates: !v })} notify={notify} />
        </Section>

        <Section title={t("settingsDialog.externalTools")}>
          <p className="text-muted-foreground">{t("settingsDialog.openFilesWithThesePrograms")}</p>
          <p className="text-muted-foreground">
            <Trans i18nKey="settingsDialog.toolTemplateHelp" components={[<code key="a" className="font-mono">{null}</code>, <code key="b" className="font-mono">{null}</code>, <code key="c" className="font-mono">{null}</code>]} />
          </p>
          {draft.tools.map((tool, i) => (
            <div key={i} className="flex items-center gap-2">
              <input
                aria-label={translate("settingsDialog.extension", { value: i + 1 })}
                value={tool.extension}
                onChange={(e) => setTool(i, { extension: e.target.value })}
                placeholder={t("settingsDialog.blend")}
                className="w-20 rounded-md border border-border bg-background px-2 py-1 font-mono outline-none focus:border-accent"
              />
              <input
                aria-label={translate("settingsDialog.program", { value: i + 1 })}
                value={tool.command}
                onChange={(e) => setTool(i, { command: e.target.value })}
                placeholder={t("settingsDialog.usrBinBlender")}
                className="min-w-0 flex-1 rounded-md border border-border bg-background px-2 py-1 font-mono outline-none focus:border-accent"
              />
              <Button type="button" variant="ghost" size="icon" onClick={() => browse(i)} aria-label={translate("settingsDialog.chooseProgram", { value: i + 1 })} title={t("settingsDialog.chooseAProgram")}>
                <FolderOpen />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                onClick={() => set({ tools: draft.tools.filter((_, j) => j !== i) })}
                aria-label={translate("settingsDialog.removeTool", { value: i + 1 })}
                title={t("settingsDialog.remove")}
              >
                <X />
              </Button>
            </div>
          ))}
          <Button type="button" size="sm" onClick={() => set({ tools: [...draft.tools, { extension: "", command: "" }] })}>
            <Plus />
            {t("settingsDialog.addAProgram")}
          </Button>
        </Section>

        {save.isError && (
          <p role="alert" className="text-danger">
            {errorMessage(save.error)}
          </p>
        )}
      </div>
      <div className="flex justify-end gap-2 border-t border-border px-5 py-3">
        <Button type="button" size="sm" onClick={onClose}>
          {restartHint ? translate("settingsDialog.close") : translate("settingsDialog.cancel")}
        </Button>
        <Button type="submit" size="sm" variant="primary" disabled={!valid || save.isPending}>
          {t("settingsDialog.save")}
        </Button>
      </div>
    </form>
  );
}

export function pollChoices(current: number): { seconds: number; label: string }[] {
  if (pollOptions.some((o) => o.seconds === current)) return pollOptions;
  return [...pollOptions, { seconds: current, label: translate("settingsDialog.seconds", { current }) }].sort((a, b) => a.seconds - b.seconds);
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="space-y-2">
      <h3 className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{title}</h3>
      {children}
    </section>
  );
}

function Check({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex items-center gap-2">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} className="size-3.5 accent-[var(--accent)]" />
      {label}
    </label>
  );
}

function InterfaceSection({ onTour, canReplay }: { onTour: () => void; canReplay: boolean }) {
  const { t } = useTranslation();
  const mode = useMode();
  const simple = mode === "simple";
  const [language, setLanguage] = useState<LanguageSetting>(storedLanguage);
  return (
    <>
      <label className="flex items-center gap-2">
        <span className="font-medium">{t("settingsDialog.language")}</span>
        <select
          value={language}
          onChange={(e) => {
            const next = e.target.value as LanguageSetting;
            setLanguage(next);
            void changeLanguage(next);
          }}
          className="h-7 rounded-md border border-border bg-background px-2 text-xs"
        >
          <option value="system">{t("settingsDialog.languageSystem")}</option>
          {LANGUAGES.map((code) => (
            <option key={code} value={code}>
              {LANGUAGE_NAMES[code]}
            </option>
          ))}
        </select>
      </label>
      <label className="flex items-start gap-2">
        <input type="checkbox" className="mt-0.5" checked={simple} onChange={(e) => setMode(e.target.checked ? "simple" : "full")} />
        <span>
          {t("settingsDialog.simpleMode")}
          <span className="block text-muted-foreground">{t("settingsDialog.fewerTabsAndPlainWords")}</span>
        </span>
      </label>
      <Button
        type="button"
        size="sm"
        disabled={!canReplay}
        title={canReplay ? undefined : translate("settingsDialog.openOrCloneAProject")}
        onClick={() => {
          startTour();
          onTour();
        }}
      >
        {t("settingsDialog.replayTheQuickTour")}
      </Button>
    </>
  );
}
