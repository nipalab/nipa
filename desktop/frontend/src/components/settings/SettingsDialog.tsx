import { useEffect, useId, useRef, useState, type FormEvent, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AppWindow, Bell, Download, FolderOpen, Gauge, Monitor, Moon, Palette, Plus, RotateCw, Settings2, Sparkles, Sun, X, type LucideIcon } from "lucide-react";
import { desktop, type LLMSettings, type Settings, type Tool } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { UpdatesSection } from "@/components/settings/UpdatesSection";
import { AISection, aiFormValid, type AIForm } from "@/components/settings/AISection";
import { fieldClass, Group, Row, SwitchRow } from "@/components/settings/SettingsLayout";
import { llmSettingsKey, useLLMSettings } from "@/hooks/useGenerate";
import { setMode, startTour, useMode } from "@/lib/mode";
import { useTheme, type Theme } from "@/lib/theme";
import { cn, errorMessage } from "@/lib/utils";
import { formatZoom, setZoom, useZoom, ZOOM_STEPS } from "@/lib/zoom";
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
      <div role="dialog" aria-modal="true" aria-labelledby="settings-title" className="flex h-[min(40rem,100%)] w-full max-w-3xl flex-col overflow-hidden rounded-lg border border-border bg-card shadow-xl">
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
            {t("settingsDialog.loadFailed", {
              error: errorMessage(settings.error ?? workers.error ?? llm.error),
            })}
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
  const [ai, setAI] = useState<AIForm>({
    endpoint: initialLLM.endpoint,
    model: initialLLM.model,
    key: "",
  });
  const [section, setSection] = useState<SectionId>("general");
  const ids = { workers: useId(), poll: useId(), service: useId() };
  const set = (patch: Partial<Settings>) => setDraft((d) => ({ ...d, ...patch }));
  const setNotify = (patch: Partial<Settings["notify"]>) => set({ notify: { ...draft.notify, ...patch } });
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
        setSection("advanced");
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
    onError: (err) =>
      notify(
        translate("settingsDialog.couldNotRestartTheBackground", {
          value: errorMessage(err),
        }),
      ),
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
    <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-0 flex-1">
        <SectionNav current={section} onSelect={setSection} invalid={{ advanced: !workersValid, aIAssistance: !aiFormValid(ai) }} />
        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5 text-[12px]">
          <Panel id="general" active={section === "general"}>
            <GeneralSection draft={draft} set={set} onTour={onClose} canReplay={canReplayTour} />
          </Panel>

          <Panel id="appearance" active={section === "appearance"}>
            <Group>
              <Row label={t("settingsDialog.theme")} description={t("settingsDialog.themeHint")}>
                <ThemeChoice />
              </Row>
              <ZoomRow />
            </Group>
          </Panel>

          <Panel id="notifications" active={section === "notifications"}>
            <Group title={t("settingsDialog.notifyMeWhen")}>
              <SwitchRow label={t("settingsDialog.teammatesPushToMyBranch")} checked={draft.notify.pushes} onChange={(v) => setNotify({ pushes: v })} />
              <SwitchRow label={t("settingsDialog.aLockIsReleased")} checked={draft.notify.locks} onChange={(v) => setNotify({ locks: v })} />
              <SwitchRow label={t("settingsDialog.someoneAsksMeToRelease")} checked={draft.notify.lockRequests} onChange={(v) => setNotify({ lockRequests: v })} />
              <SwitchRow label={t("settingsDialog.someoneAsksForMyReview")} checked={draft.notify.reviews} onChange={(v) => setNotify({ reviews: v })} />
              <SwitchRow label={t("settingsDialog.maintenanceIsScheduled")} checked={draft.notify.maintenance} onChange={(v) => setNotify({ maintenance: v })} />
            </Group>
            <Group>
              <Row label={t("settingsDialog.checkTheServerEvery")} description={t("settingsDialog.pollHint")} htmlFor={ids.poll}>
                <select id={ids.poll} value={draft.pollSeconds} onChange={(e) => set({ pollSeconds: Number(e.target.value) })} className={fieldClass}>
                  {pollChoices(draft.pollSeconds).map((o) => (
                    <option key={o.seconds} value={o.seconds}>
                      {o.label}
                    </option>
                  ))}
                </select>
              </Row>
            </Group>
          </Panel>

          <Panel id="updates" active={section === "updates"}>
            <UpdatesSection auto={!draft.manualUpdates} onAuto={(v) => set({ manualUpdates: !v })} notify={notify} />
          </Panel>

          <Panel id="aIAssistance" active={section === "aIAssistance"}>
            <AISection value={ai} onChange={(patch) => setAI((f) => ({ ...f, ...patch }))} keySaved={initialLLM.keySaved} configured={initialLLM.configured} notify={notify} />
          </Panel>

          <Panel id="externalTools" active={section === "externalTools"}>
            <Group
              footer={
                <Trans
                  i18nKey="settingsDialog.toolTemplateHelp"
                  components={[
                    <code key="a" className="font-mono text-foreground">
                      {null}
                    </code>,
                    <code key="b" className="font-mono text-foreground">
                      {null}
                    </code>,
                    <code key="c" className="font-mono text-foreground">
                      {null}
                    </code>,
                  ]}
                />
              }
            >
              {draft.tools.length === 0 && <p className="px-4 py-3 text-muted-foreground">{t("settingsDialog.noTools")}</p>}
              {draft.tools.map((tool, i) => (
                <div key={i} className="flex items-center gap-2 px-3 py-2">
                  <input
                    aria-label={translate("settingsDialog.extension", {
                      value: i + 1,
                    })}
                    value={tool.extension}
                    onChange={(e) => setTool(i, { extension: e.target.value })}
                    placeholder={t("settingsDialog.blend")}
                    className={cn(fieldClass, "w-24 font-mono")}
                  />
                  <input
                    aria-label={translate("settingsDialog.program", {
                      value: i + 1,
                    })}
                    value={tool.command}
                    onChange={(e) => setTool(i, { command: e.target.value })}
                    placeholder={t("settingsDialog.usrBinBlender")}
                    className={cn(fieldClass, "min-w-0 flex-1 font-mono")}
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    onClick={() => browse(i)}
                    aria-label={translate("settingsDialog.chooseProgram", {
                      value: i + 1,
                    })}
                    title={t("settingsDialog.chooseAProgram")}
                  >
                    <FolderOpen />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    onClick={() => set({ tools: draft.tools.filter((_, j) => j !== i) })}
                    aria-label={translate("settingsDialog.removeTool", {
                      value: i + 1,
                    })}
                    title={t("settingsDialog.remove")}
                  >
                    <X />
                  </Button>
                </div>
              ))}
            </Group>
            <Button type="button" size="sm" onClick={() => set({ tools: [...draft.tools, { extension: "", command: "" }] })}>
              <Plus />
              {t("settingsDialog.addAProgram")}
            </Button>
          </Panel>

          <Panel id="advanced" active={section === "advanced"}>
            <Group title={t("settingsDialog.transfers")}>
              <Row
                label={t("settingsDialog.uploadsAtOnce")}
                description={workersValid ? t("settingsDialog.leaveEmptyToTuneIt") : t("settingsDialog.enterANumberFrom1")}
                tone={workersValid ? "default" : "danger"}
                htmlFor={ids.workers}
              >
                <input
                  id={ids.workers}
                  inputMode="numeric"
                  value={workers}
                  onChange={(e) => setWorkers(e.target.value)}
                  placeholder={t("settingsDialog.automatic")}
                  aria-invalid={!workersValid}
                  className={cn(fieldClass, "w-28 text-right")}
                />
              </Row>
            </Group>
            <Group title={t("settingsDialog.backgroundService")}>
              <Row
                label={t("settingsDialog.restartBackgroundService")}
                description={restartHint ? t("settingsDialog.restartTheBackgroundServiceTo") : t("settingsDialog.backgroundServiceHint")}
                tone={restartHint ? "accent" : "default"}
                labelId={ids.service}
              >
                <Button type="button" size="sm" variant={restartHint ? "primary" : undefined} aria-describedby={ids.service} onClick={() => restart.mutate()} disabled={restart.isPending}>
                  <RotateCw />
                  {t("settingsDialog.restart")}
                </Button>
              </Row>
            </Group>
          </Panel>
        </div>
      </div>
      <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
        {save.isError && (
          <p role="alert" className="mr-auto min-w-0 truncate text-[12px] text-danger" title={errorMessage(save.error)}>
            {errorMessage(save.error)}
          </p>
        )}
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
  return [
    ...pollOptions,
    {
      seconds: current,
      label: translate("settingsDialog.seconds", { current }),
    },
  ].sort((a, b) => a.seconds - b.seconds);
}

const SECTIONS = [
  { id: "general", Icon: Settings2 },
  { id: "appearance", Icon: Palette },
  { id: "notifications", Icon: Bell },
  { id: "updates", Icon: Download },
  { id: "aIAssistance", Icon: Sparkles },
  { id: "externalTools", Icon: AppWindow },
  { id: "advanced", Icon: Gauge },
] as const satisfies readonly { id: string; Icon: LucideIcon }[];

type SectionId = (typeof SECTIONS)[number]["id"];

const sectionTitle = (id: SectionId) => translate(`settingsDialog.${id}`);
const sectionHint = (id: SectionId) => translate(`settingsDialog.${id}Hint`);

function SectionNav({ current, onSelect, invalid }: { current: SectionId; onSelect: (id: SectionId) => void; invalid: Partial<Record<SectionId, boolean>> }) {
  const { t } = useTranslation();
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const onKeyDown = (e: ReactKeyboardEvent, i: number) => {
    const last = SECTIONS.length - 1;
    const next = e.key === "ArrowDown" ? (i === last ? 0 : i + 1) : e.key === "ArrowUp" ? (i === 0 ? last : i - 1) : e.key === "Home" ? 0 : e.key === "End" ? last : -1;
    if (next < 0) return;
    e.preventDefault();
    onSelect(SECTIONS[next].id);
    refs.current[next]?.focus();
  };
  return (
    <nav role="tablist" aria-orientation="vertical" aria-label={t("settingsDialog.settings")} className="flex w-48 shrink-0 flex-col gap-0.5 overflow-y-auto border-r border-border bg-background/30 p-2">
      {SECTIONS.map(({ id, Icon }, i) => (
        <button
          key={id}
          ref={(el) => {
            refs.current[i] = el;
          }}
          id={`settings-tab-${id}`}
          type="button"
          role="tab"
          aria-selected={current === id}
          aria-controls={`settings-panel-${id}`}
          tabIndex={current === id ? 0 : -1}
          onClick={() => onSelect(id)}
          onKeyDown={(e) => onKeyDown(e, i)}
          className={cn(
            "flex items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-[12px] transition-colors",
            current === id ? "bg-accent-soft font-medium text-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground",
          )}
        >
          <Icon className={cn("size-3.5 shrink-0", current === id && "text-accent")} />
          <span className="min-w-0 flex-1 truncate">{sectionTitle(id)}</span>
          {invalid[id] && <span aria-hidden className="size-1.5 rounded-full bg-danger" />}
        </button>
      ))}
    </nav>
  );
}

function Panel({ id, active, children }: { id: SectionId; active: boolean; children: ReactNode }) {
  return (
    <section id={`settings-panel-${id}`} role="tabpanel" aria-labelledby={`settings-tab-${id}`} hidden={!active} className="space-y-5">
      <header>
        <h3 className="text-sm font-semibold">{sectionTitle(id)}</h3>
        <p className="mt-0.5 text-muted-foreground">{sectionHint(id)}</p>
      </header>
      {children}
    </section>
  );
}

const themes: { value: Theme; label: string; Icon: LucideIcon }[] = [
  { value: "light", label: translate("themeToggle.light"), Icon: Sun },
  { value: "dark", label: translate("themeToggle.dark"), Icon: Moon },
  { value: "system", label: translate("themeToggle.system"), Icon: Monitor },
];

function ThemeChoice() {
  const { t } = useTranslation();
  const { theme, setTheme } = useTheme();
  return (
    <div role="radiogroup" aria-label={t("themeToggle.theme")} className="flex rounded-md border border-border bg-background p-0.5">
      {themes.map(({ value, label, Icon }) => (
        <button
          key={value}
          type="button"
          role="radio"
          aria-checked={theme === value}
          onClick={() => setTheme(value)}
          className={cn("flex h-7 items-center gap-1.5 rounded px-2.5 text-[12px] text-muted-foreground transition-colors hover:text-foreground", theme === value && "bg-muted font-medium text-foreground")}
        >
          <Icon className="size-3.5" />
          {label}
        </button>
      ))}
    </div>
  );
}

function ZoomRow() {
  const { t } = useTranslation();
  const zoom = useZoom();
  const id = useId();
  return (
    <Row label={t("settingsDialog.zoom")} description={t("settingsDialog.zoomHint")} htmlFor={id}>
      <Button type="button" size="sm" variant="ghost" disabled={zoom === 1} onClick={() => setZoom(1)}>
        {t("settingsDialog.resetZoom")}
      </Button>
      <select id={id} value={zoom} onChange={(e) => setZoom(Number(e.target.value))} className={fieldClass}>
        {ZOOM_STEPS.map((z) => (
          <option key={z} value={z}>
            {formatZoom(z)}
          </option>
        ))}
      </select>
    </Row>
  );
}

function GeneralSection({ draft, set, onTour, canReplay }: { draft: Settings; set: (patch: Partial<Settings>) => void; onTour: () => void; canReplay: boolean }) {
  const { t } = useTranslation();
  const mode = useMode();
  const [language, setLanguage] = useState<LanguageSetting>(storedLanguage);
  const ids = { language: useId(), tour: useId() };
  return (
    <>
      <Group>
        <Row label={t("settingsDialog.language")} description={t("settingsDialog.languageHint")} htmlFor={ids.language}>
          <select
            id={ids.language}
            value={language}
            onChange={(e) => {
              const next = e.target.value as LanguageSetting;
              setLanguage(next);
              void changeLanguage(next);
            }}
            className={fieldClass}
          >
            <option value="system">{t("settingsDialog.languageSystem")}</option>
            {LANGUAGES.map((code) => (
              <option key={code} value={code}>
                {LANGUAGE_NAMES[code]}
              </option>
            ))}
          </select>
        </Row>
        <SwitchRow label={t("settingsDialog.simpleMode")} description={t("settingsDialog.fewerTabsAndPlainWords")} checked={mode === "simple"} onChange={(v) => setMode(v ? "simple" : "full")} />
        <Row label={t("settingsDialog.quickTour")} description={canReplay ? t("settingsDialog.quickTourHint") : t("settingsDialog.openOrCloneAProject")} labelId={ids.tour}>
          <Button
            type="button"
            size="sm"
            disabled={!canReplay}
            aria-describedby={ids.tour}
            onClick={() => {
              startTour();
              onTour();
            }}
          >
            {t("settingsDialog.replay")}
          </Button>
        </Row>
      </Group>
      <Group title={t("settingsDialog.startup")}>
        <SwitchRow label={t("settingsDialog.startNipaWhenILog")} description={t("settingsDialog.startAtLoginHint")} checked={draft.startAtLogin} onChange={(v) => set({ startAtLogin: v })} />
        <SwitchRow label={t("settingsDialog.keepInTray")} description={t("settingsDialog.keepInTrayHint")} checked={draft.keepInTray} onChange={(v) => set({ keepInTray: v })} />
      </Group>
    </>
  );
}
