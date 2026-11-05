import { useEffect, useId, useState, type FormEvent, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FolderOpen, Plus, RotateCw, X } from "lucide-react";
import { desktop, type Settings, type Tool } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { ThemeToggle } from "@/components/ThemeToggle";
import { errorMessage } from "@/lib/utils";

export const pollOptions: { seconds: number; label: string }[] = [
  { seconds: 30, label: "30 seconds" },
  { seconds: 60, label: "1 minute" },
  { seconds: 300, label: "5 minutes" },
  { seconds: 900, label: "15 minutes" },
  { seconds: 3600, label: "1 hour" },
];

type SettingsDialogProps = {
  onClose: () => void;
  notify: (message: string, tone?: "error" | "success") => void;
};

export function SettingsDialog({ onClose, notify }: SettingsDialogProps) {
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
  return (
    <div role="presentation" className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4">
      <div role="dialog" aria-modal="true" aria-labelledby="settings-title" className="flex max-h-full w-full max-w-lg flex-col rounded-lg border border-border bg-card shadow-xl">
        <div className="flex items-center justify-between border-b border-border px-5 py-3">
          <h2 id="settings-title" className="text-sm font-semibold">
            Settings
          </h2>
          <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close settings">
            <X />
          </Button>
        </div>
        {settings.isError || workers.isError ? (
          <p role="alert" className="p-5 text-xs text-danger">
            Could not load settings: {errorMessage(settings.error ?? workers.error)}
          </p>
        ) : settings.data && workers.data !== undefined ? (
          <SettingsForm initial={settings.data} initialWorkers={workers.data} onClose={onClose} notify={notify} />
        ) : (
          <p role="status" className="p-5 text-xs text-muted-foreground">
            Loading settings…
          </p>
        )}
      </div>
    </div>
  );
}

type FormProps = {
  initial: Settings;
  initialWorkers: number;
  onClose: () => void;
  notify: SettingsDialogProps["notify"];
};

function SettingsForm({ initial, initialWorkers, onClose, notify }: FormProps) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<Settings>(initial);
  const [workers, setWorkers] = useState(initialWorkers > 0 ? String(initialWorkers) : "");
  const [restartHint, setRestartHint] = useState(false);
  const ids = { workers: useId(), poll: useId() };
  const set = (patch: Partial<Settings>) => setDraft((d) => ({ ...d, ...patch }));
  const setTool = (i: number, patch: Partial<Tool>) =>
    set({
      tools: draft.tools.map((t, j) => (j === i ? { ...t, ...patch } : t)),
    });
  const workerCount = workers.trim() === "" ? 0 : Number(workers);
  const workersValid = Number.isInteger(workerCount) && workerCount >= 0 && workerCount <= 64;

  const save = useMutation({
    mutationFn: async () => {
      const saved = await desktop.saveSettings(draft);
      if (workerCount !== initialWorkers) await desktop.setUploadWorkers(workerCount);
      return saved;
    },
    onSuccess: (saved) => {
      queryClient.setQueryData(["settings"], saved);
      queryClient.setQueryData(["upload-workers"], workerCount);
      if (workerCount !== initialWorkers) {
        setRestartHint(true);
        setDraft(saved);
        return;
      }
      notify("Settings saved", "success");
      onClose();
    },
  });

  const restart = useMutation({
    mutationFn: desktop.restartDaemon,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["daemon"] });
      void queryClient.invalidateQueries({ queryKey: ["repos"] });
      notify("Background service restarted", "success");
      onClose();
    },
    onError: (err) => notify(`Could not restart the background service: ${errorMessage(err)}`),
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
    if (workersValid) save.mutate();
  };

  return (
    <form onSubmit={submit} className="flex min-h-0 flex-col">
      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-4 text-[12px]">
        <Section title="Appearance">
          <div className="flex items-center justify-between">
            <span>Theme</span>
            <ThemeToggle />
          </div>
        </Section>

        <Section title="Background">
          <Check label="Keep Nipa running in the tray when the window is closed" checked={draft.keepInTray} onChange={(v) => set({ keepInTray: v })} />
          <Check label="Start Nipa when I log in" checked={draft.startAtLogin} onChange={(v) => set({ startAtLogin: v })} />
        </Section>

        <Section title="Notifications">
          <Check label="Teammates push to my branch" checked={draft.notify.pushes} onChange={(v) => set({ notify: { ...draft.notify, pushes: v } })} />
          <Check label="A lock is released" checked={draft.notify.locks} onChange={(v) => set({ notify: { ...draft.notify, locks: v } })} />
          <Check label="Someone asks for my review" checked={draft.notify.reviews} onChange={(v) => set({ notify: { ...draft.notify, reviews: v } })} />
          <div className="flex items-center justify-between gap-3 pt-1">
            <label htmlFor={ids.poll}>Check the server every</label>
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

        <Section title="Transfers">
          <div className="flex items-center justify-between gap-3">
            <label htmlFor={ids.workers}>Uploads at once</label>
            <input
              id={ids.workers}
              inputMode="numeric"
              value={workers}
              onChange={(e) => setWorkers(e.target.value)}
              placeholder="Automatic"
              aria-invalid={!workersValid}
              className="w-28 rounded-md border border-border bg-background px-2 py-1 text-right outline-none focus:border-accent aria-[invalid=true]:border-danger"
            />
          </div>
          <p className="text-muted-foreground">Leave empty to tune it automatically. Shared with the nipa command line.</p>
          {!workersValid && <p className="text-danger">Enter a number from 1 to 64, or leave it empty.</p>}
          {restartHint && (
            <div role="status" className="flex items-center justify-between gap-3 rounded-md bg-accent-soft px-3 py-2">
              <span>Restart the background service to use the new upload setting.</span>
              <Button type="button" size="sm" onClick={() => restart.mutate()} disabled={restart.isPending}>
                <RotateCw />
                Restart
              </Button>
            </div>
          )}
        </Section>

        <Section title="External tools">
          <p className="text-muted-foreground">Open files with these programs instead of the system default, for example .blend with Blender.</p>
          {draft.tools.map((tool, i) => (
            <div key={i} className="flex items-center gap-2">
              <input
                aria-label={`Extension ${i + 1}`}
                value={tool.extension}
                onChange={(e) => setTool(i, { extension: e.target.value })}
                placeholder=".blend"
                className="w-20 rounded-md border border-border bg-background px-2 py-1 font-mono outline-none focus:border-accent"
              />
              <input
                aria-label={`Program ${i + 1}`}
                value={tool.command}
                onChange={(e) => setTool(i, { command: e.target.value })}
                placeholder="/usr/bin/blender"
                className="min-w-0 flex-1 rounded-md border border-border bg-background px-2 py-1 font-mono outline-none focus:border-accent"
              />
              <Button type="button" variant="ghost" size="icon" onClick={() => browse(i)} aria-label={`Choose program ${i + 1}`} title="Choose a program">
                <FolderOpen />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                onClick={() => set({ tools: draft.tools.filter((_, j) => j !== i) })}
                aria-label={`Remove tool ${i + 1}`}
                title="Remove"
              >
                <X />
              </Button>
            </div>
          ))}
          <Button type="button" size="sm" onClick={() => set({ tools: [...draft.tools, { extension: "", command: "" }] })}>
            <Plus />
            Add a program
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
          {restartHint ? "Close" : "Cancel"}
        </Button>
        <Button type="submit" size="sm" variant="primary" disabled={!workersValid || save.isPending}>
          Save
        </Button>
      </div>
    </form>
  );
}

export function pollChoices(current: number): { seconds: number; label: string }[] {
  if (pollOptions.some((o) => o.seconds === current)) return pollOptions;
  return [...pollOptions, { seconds: current, label: `${current} seconds` }].sort((a, b) => a.seconds - b.seconds);
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
