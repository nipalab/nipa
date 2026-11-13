import { useCallback, useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PanelLeftOpen } from "lucide-react";
import { desktop, type Navigation, type Repo, type RepoActivity } from "@/api/desktop";
import { CloneDialog, type CloneHint } from "@/components/clone/CloneDialog";
import { CommandPalette, type Go, type PaletteMode } from "@/components/palette/CommandPalette";
import { ShortcutsSheet } from "@/components/palette/ShortcutsSheet";
import { RepoView, type Intent, type Notify } from "@/components/RepoView";
import { SettingsDialog } from "@/components/settings/SettingsDialog";
import { Sidebar } from "@/components/Sidebar";
import { DaemonError, Splash } from "@/components/StatusScreens";
import { Toast } from "@/components/Toast";
import { Button } from "@/components/ui/button";
import { UpdateBanner } from "@/components/UpdateBanner";
import { Welcome } from "@/components/Welcome";
import { useShortcuts } from "@/hooks/useShortcuts";
import { errorMessage, readSetting, writeSetting } from "@/lib/utils";

const SELECTED_KEY = "nipa.selectedRepo";
const SIDEBAR_HIDDEN_KEY = "nipa.layout.sidebar-hidden";

// refreshOn lists the views a teammate event makes stale.
const refreshOn: Record<string, string[]> = {
  pushed: ["history", "branches"],
  lock_released: ["locks"],
  review_requested: ["mrs"],
};

export default function App() {
  const queryClient = useQueryClient();
  const [selected, setSelectedState] = useState<string | null>(() => readSetting(SELECTED_KEY));
  const [cloning, setCloning] = useState(false);
  const [cloneHint, setCloneHint] = useState<CloneHint | undefined>();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [palette, setPalette] = useState<PaletteMode | null>(null);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  const [sidebarHidden, setSidebarHiddenState] = useState(() => readSetting(SIDEBAR_HIDDEN_KEY) === "1");
  const setSidebarHidden = useCallback((hidden: boolean) => {
    writeSetting(SIDEBAR_HIDDEN_KEY, hidden ? "1" : null);
    setSidebarHiddenState(hidden);
  }, []);
  const [intent, setIntent] = useState<(Intent & { root: string }) | undefined>();
  const seq = useRef(0);
  const [toast, setToastState] = useState<{ message: string; tone: "error" | "success" } | null>(null);
  const notify = useCallback<Notify>((message, tone = "error") => setToastState({ message, tone }), []);
  const dismiss = useCallback(() => setToastState(null), []);
  const setToast = (message: string) => notify(message);

  const daemon = useQuery({ queryKey: ["daemon"], queryFn: desktop.connect, retry: false });
  const repos = useQuery({ queryKey: ["repos"], queryFn: desktop.repos, enabled: daemon.isSuccess });
  const activity = useQuery({ queryKey: ["activity"], queryFn: desktop.activity, enabled: daemon.isSuccess });

  useEffect(
    () =>
      desktop.onRepoActivity((a) => {
        queryClient.setQueryData<RepoActivity[]>(["activity"], (old = []) => [...old.filter((x) => x.root !== a.root), { ...a, events: [] }]);
        const keys = new Set(a.events.flatMap((e) => refreshOn[e.kind] ?? []));
        for (const key of keys) void queryClient.invalidateQueries({ queryKey: [key, a.root] });
      }),
    [queryClient],
  );

  const ready = repos.isSuccess;
  useShortcuts(
    {
      palette: () => setPalette("all"),
      help: () => setShortcutsOpen(true),
      settings: () => setSettingsOpen(true),
      sidebar: () =>
        setSidebarHiddenState((hidden) => {
          writeSetting(SIDEBAR_HIDDEN_KEY, hidden ? null : "1");
          return !hidden;
        }),
    },
    ready,
  );

  const select = (root: string | null) => {
    writeSetting(SELECTED_KEY, root);
    setSelectedState(root);
    setIntent(undefined);
  };
  const go: Go = (root, next) => {
    select(root);
    seq.current += 1;
    setIntent({ ...next, root, seq: seq.current });
  };

  const navigate = useRef<(nav: Navigation) => void>(() => {});
  navigate.current = (nav: Navigation) => {
    if (!nav.root && nav.tab === "settings") {
      setSettingsOpen(true);
      return;
    }
    if (nav.root) {
      go(nav.root, { tab: nav.tab, number: nav.number || undefined, commit: nav.commit || undefined, path: nav.path || undefined });
      return;
    }
    if (nav.project) {
      notify(`${nav.org}/${nav.project} is not cloned on this computer yet`);
      setCloneHint({ server: nav.server, project: nav.project });
      setCloning(true);
    }
  };
  useEffect(() => {
    if (!ready) return;
    const take = () =>
      desktop.takeNavigation().then(
        (nav) => nav && navigate.current(nav),
        () => {},
      );
    void take();
    return desktop.onNavigate(() => void take());
  }, [ready]);

  const remember = (repo: Repo) => {
    queryClient.setQueryData<Repo[]>(["repos"], (old = []) => (old.some((r) => r.root === repo.root) ? old.map((r) => (r.root === repo.root ? repo : r)) : [...old, repo]));
    select(repo.root);
  };

  const add = useMutation({
    mutationFn: desktop.pickRepo,
    onSuccess: (repo) => {
      if (repo) remember(repo);
    },
    onError: (err) => setToast(errorMessage(err)),
  });

  const remove = useMutation({
    mutationFn: desktop.removeRepo,
    onSuccess: (_, root) => {
      queryClient.setQueryData<Repo[]>(["repos"], (old = []) => old.filter((r) => r.root !== root));
      if (selected === root) select(null);
    },
    onError: (err) => setToast(errorMessage(err)),
  });

  if (daemon.isPending) return <Splash label="Starting Nipa…" />;
  if (daemon.isError) {
    return <DaemonError message={errorMessage(daemon.error)} retrying={daemon.isFetching} onRetry={() => daemon.refetch()} />;
  }
  if (repos.isPending) return <Splash label="Loading repositories…" />;
  if (repos.isError) {
    return <DaemonError message={errorMessage(repos.error)} retrying={repos.isFetching} onRetry={() => repos.refetch()} />;
  }

  const list = repos.data;
  const current = list.find((r) => r.root === selected) ?? list[0];
  const pending = (activity.data ?? []).some((a) => a.incoming > 0 || a.reviewRequests > 0);
  const showSidebar = sidebarHidden ? (
    <Button variant="ghost" size="icon" className="relative shrink-0" onClick={() => setSidebarHidden(false)} aria-label="Show sidebar" title="Show sidebar (Ctrl+B)">
      <PanelLeftOpen />
      {pending && <span className="absolute right-1 top-1 size-1.5 rounded-full bg-accent" aria-label="New activity" />}
    </Button>
  ) : null;

  return (
    <div className="flex h-full">
      {!sidebarHidden && (
        <Sidebar
          repos={list}
          selected={current?.root ?? null}
          daemon={daemon.data}
          adding={add.isPending}
          onSelect={select}
          onAdd={() => add.mutate()}
          onClone={() => setCloning(true)}
          onSettings={() => setSettingsOpen(true)}
          activity={activity.data ?? []}
          onHide={() => setSidebarHidden(true)}
        />
      )}
      <main className="flex min-w-0 flex-1 flex-col">
        <UpdateBanner notify={notify} />
        <div className="min-h-0 flex-1">
          {current ? (
            <RepoView
              key={current.root}
              repo={current}
              removing={remove.isPending}
              onRemove={(root) => remove.mutate(root)}
              notify={notify}
              activity={activity.data?.find((a) => a.root === current.root)}
              intent={intent?.root === current.root ? intent : undefined}
              leading={showSidebar}
              onSearch={() => setPalette("all")}
            />
          ) : (
            <Welcome adding={add.isPending} onAdd={() => add.mutate()} onClone={() => setCloning(true)} leading={showSidebar} />
          )}
        </div>
      </main>
      {cloning && (
        <CloneDialog
          hint={cloneHint}
          onClose={() => {
            setCloning(false);
            setCloneHint(undefined);
          }}
          onCloned={(repo) => {
            setCloning(false);
            setCloneHint(undefined);
            remember(repo);
            notify(`Cloned ${repo.name}`, "success");
          }}
        />
      )}
      {settingsOpen && <SettingsDialog onClose={() => setSettingsOpen(false)} notify={notify} />}
      {palette && (
        <CommandPalette
          repos={list}
          current={current}
          initialMode={palette}
          onClose={() => setPalette(null)}
          onGo={go}
          onSettings={() => setSettingsOpen(true)}
          onShortcuts={() => setShortcutsOpen(true)}
        />
      )}
      {shortcutsOpen && <ShortcutsSheet onClose={() => setShortcutsOpen(false)} />}
      {toast && <Toast message={toast.message} tone={toast.tone} onDismiss={dismiss} />}
    </div>
  );
}
