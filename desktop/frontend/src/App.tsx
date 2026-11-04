import { useCallback, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { desktop, type Repo } from "@/api/desktop";
import { CloneDialog } from "@/components/clone/CloneDialog";
import { RepoView, type Notify } from "@/components/RepoView";
import { Sidebar } from "@/components/Sidebar";
import { DaemonError, Splash } from "@/components/StatusScreens";
import { Toast } from "@/components/Toast";
import { Welcome } from "@/components/Welcome";
import { errorMessage, readSetting, writeSetting } from "@/lib/utils";

const SELECTED_KEY = "nipa.selectedRepo";

export default function App() {
  const queryClient = useQueryClient();
  const [selected, setSelectedState] = useState<string | null>(() => readSetting(SELECTED_KEY));
  const [cloning, setCloning] = useState(false);
  const [toast, setToastState] = useState<{ message: string; tone: "error" | "success" } | null>(null);
  const notify = useCallback<Notify>((message, tone = "error") => setToastState({ message, tone }), []);
  const dismiss = useCallback(() => setToastState(null), []);
  const setToast = (message: string) => notify(message);

  const daemon = useQuery({ queryKey: ["daemon"], queryFn: desktop.connect, retry: false });
  const repos = useQuery({ queryKey: ["repos"], queryFn: desktop.repos, enabled: daemon.isSuccess });

  const select = (root: string | null) => {
    writeSetting(SELECTED_KEY, root);
    setSelectedState(root);
  };

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

  return (
    <div className="flex h-full">
      <Sidebar
        repos={list}
        selected={current?.root ?? null}
        daemon={daemon.data}
        adding={add.isPending}
        onSelect={select}
        onAdd={() => add.mutate()}
        onClone={() => setCloning(true)}
      />
      <main className="min-w-0 flex-1">
        {current ? (
          <RepoView key={current.root} repo={current} removing={remove.isPending} onRemove={(root) => remove.mutate(root)} notify={notify} />
        ) : (
          <Welcome adding={add.isPending} onAdd={() => add.mutate()} onClone={() => setCloning(true)} />
        )}
      </main>
      {cloning && (
        <CloneDialog
          onClose={() => setCloning(false)}
          onCloned={(repo) => {
            setCloning(false);
            remember(repo);
            notify(`Cloned ${repo.name}`, "success");
          }}
        />
      )}
      {toast && <Toast message={toast.message} tone={toast.tone} onDismiss={dismiss} />}
    </div>
  );
}
