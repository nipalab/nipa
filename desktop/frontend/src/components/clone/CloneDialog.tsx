import { useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { ArrowLeft, FolderOpen, KeyRound, Loader2, Search, Server, X } from "lucide-react";
import { desktop, type Account, type Cancellable, type OpProgress, type RemoteProject, type Repo } from "@/api/desktop";
import { SparsePicker } from "@/components/clone/SparsePicker";
import { Button } from "@/components/ui/button";
import { describeProgress } from "@/hooks/useRepoOperation";
import { joinPath } from "@/lib/sparse";
import { errorMessage, readSetting, writeSetting } from "@/lib/utils";
import { maintenanceOf } from "@/lib/maintenance";
import { useTranslation } from "react-i18next";
import { translate } from "@/i18n";

const SERVER_KEY = "nipa.lastServer";
const PARENT_KEY = "nipa.cloneParent";

type Step = "server" | "signin" | "project" | "options" | "cloning";

// CloneHint prefills the dialog, e.g. from a nipa:// link to a project that is
// not cloned yet.
export type CloneHint = { server: string; project: string };

type CloneDialogProps = { onClose: () => void; onCloned: (repo: Repo) => void; hint?: CloneHint };

export function CloneDialog({ onClose, onCloned, hint }: CloneDialogProps) {
  const { t } = useTranslation();
  const [step, setStep] = useState<Step>("server");

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && step !== "cloning" && onClose();
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [step, onClose]);
  const [server, setServer] = useState(() => hint?.server || (readSetting(SERVER_KEY) ?? ""));
  const [account, setAccount] = useState<Account | null>(null);
  const [project, setProject] = useState<RemoteProject | null>(null);

  const signedIn = (a: Account) => {
    setAccount(a);
    writeSetting(SERVER_KEY, server.trim());
    setStep(a.signedIn ? "project" : "signin");
  };

  const title: Record<Step, string> = {
    server: translate("cloneDialog.cloneARepository"),
    signin: translate("cloneDialog.signIn"),
    project: translate("cloneDialog.chooseAProject"),
    options: project ? translate("cloneDialog.clone2", { project: project.name || project.project }) : translate("cloneDialog.clone"),
    cloning: translate("cloneDialog.cloning"),
  };

  return (
    <div role="presentation" className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4">
      <div role="dialog" aria-modal="true" aria-labelledby="clone-title" className="flex max-h-[85vh] w-full max-w-xl flex-col rounded-lg border border-border bg-card shadow-xl">
        <header className="flex items-center gap-2 border-b border-border px-5 py-3">
          {(step === "project" || step === "options" || step === "signin") && (
            <button
              type="button"
              aria-label={t("cloneDialog.back")}
              onClick={() => setStep(step === "options" ? "project" : "server")}
              className="text-muted-foreground hover:text-foreground"
            >
              <ArrowLeft className="size-4" />
            </button>
          )}
          <h2 id="clone-title" className="flex-1 text-sm font-semibold">
            {title[step]}
          </h2>
          {account?.signedIn && step !== "server" && (
            <span className="truncate text-[11px] text-muted-foreground">{account.email ? translate("cloneDialog.text", { email: account.email, host: account.host }) : account.host}</span>
          )}
          <button type="button" aria-label={t("cloneDialog.close")} onClick={onClose} disabled={step === "cloning"} className="text-muted-foreground hover:text-foreground disabled:opacity-40">
            <X className="size-4" />
          </button>
        </header>
        <div className="min-h-0 flex-1 overflow-auto px-5 py-4">
          {step === "server" && <ServerStep server={server} onServer={setServer} onReady={signedIn} />}
          {step === "signin" && account && <SignInStep account={account} onSignedIn={() => desktop.accountStatus(account.address).then(signedIn)} />}
          {step === "project" && account && (
            <ProjectStep
              server={account.address}
              initialQuery={hint?.project}
              onPick={(p) => {
                setProject(p);
                setStep("options");
              }}
              onSignIn={() => setStep("signin")}
            />
          )}
          {(step === "options" || step === "cloning") && account && project && (
            <OptionsStep server={account.address} project={project} onSignIn={() => setStep("signin")} cloning={step === "cloning"} onCloning={(busy) => setStep(busy ? "cloning" : "options")} onCloned={onCloned} />
          )}
        </div>
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block space-y-1 text-[12px]">
      <span className="font-medium">{label}</span>
      {children}
    </label>
  );
}

const inputClass = "w-full rounded-md border border-border bg-background px-2.5 py-1.5 text-[12px] outline-none focus:ring-2 focus:ring-ring";

function ErrorText({ error }: { error: unknown }) {
  const { t } = useTranslation();
  if (!error) return null;
  const paused = maintenanceOf(error)?.mode === "frozen";
  return (
    <p role="alert" className="text-[12px] text-danger">
      {paused && <span className="block font-medium">{t("maintenance.signInPaused")}</span>}
      {errorMessage(error)}
    </p>
  );
}

function ServerStep({ server, onServer, onReady }: { server: string; onServer: (s: string) => void; onReady: (a: Account) => void }) {
  const { t } = useTranslation();
  const check = useMutation({ mutationFn: (s: string) => desktop.accountStatus(s), onSuccess: onReady });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (server.trim()) check.mutate(server.trim());
  };
  return (
    <form onSubmit={submit} className="space-y-3">
      <Field label={t("cloneDialog.server")}>
        <div className="flex items-center gap-2">
          <Server className="size-4 shrink-0 text-muted-foreground" />
          <input value={server} onChange={(e) => onServer(e.target.value)} placeholder={t("cloneDialog.httpsNipaExampleCom")} aria-label={t("cloneDialog.server")} className={inputClass} />
        </div>
      </Field>
      <p className="text-[11px] text-muted-foreground">{t("cloneDialog.theAddressOfYourTeamS")}</p>
      <ErrorText error={check.error} />
      <div className="flex justify-end">
        <Button type="submit" variant="primary" size="sm" disabled={!server.trim() || check.isPending}>
          {check.isPending && <Loader2 className="animate-spin" />}
          {t("cloneDialog.continue")}
        </Button>
      </div>
    </form>
  );
}

function SignInStep({ account, onSignedIn }: { account: Account; onSignedIn: () => Promise<void> }) {
  const { t } = useTranslation();
  const [email, setEmail] = useState(account.email);
  const [password, setPassword] = useState("");
  const ssh = useMutation({ mutationFn: () => desktop.signInSSH(account.address, email.trim()).then(onSignedIn) });
  const [challenge, setChallenge] = useState("");
  const [code, setCode] = useState("");
  const password_ = useMutation({
    mutationFn: async () => {
      const result = await desktop.signIn(account.address, email, password);
      if (result.mfaRequired) {
        setCode("");
        setChallenge(result.challenge);
        return;
      }
      await onSignedIn();
    },
  });
  const mfa = useMutation({ mutationFn: () => desktop.signInMFA(account.address, email, challenge, code).then(onSignedIn) });
  const tried = useRef(false);
  useEffect(() => {
    if (tried.current) return;
    tried.current = true;
    ssh.mutate();
  }, [ssh]);

  const busy = ssh.isPending || password_.isPending || mfa.isPending;
  if (challenge) {
    return (
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          mfa.mutate();
        }}
      >
        <p className="text-[12px] text-muted-foreground">{t("cloneDialog.enterTheCodeFromYour")}</p>
        <Field label={t("cloneDialog.authenticationCode")}>
          <input value={code} onChange={(e) => setCode(e.target.value)} autoComplete="one-time-code" inputMode="numeric" autoFocus aria-label={t("cloneDialog.authenticationCode")} className={inputClass} />
        </Field>
        <ErrorText error={mfa.error} />
        <div className="flex justify-between">
          <Button
            size="sm"
            onClick={() => {
              mfa.reset();
              setChallenge("");
            }}
            disabled={mfa.isPending}
          >
            {t("cloneDialog.startOver")}
          </Button>
          <Button type="submit" variant="primary" size="sm" disabled={mfa.isPending || !code.trim()}>
            {mfa.isPending && <Loader2 className="animate-spin" />}
            {t("cloneDialog.verify")}
          </Button>
        </div>
      </form>
    );
  }
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2 rounded-md border border-border bg-background p-3 text-[12px]">
        <KeyRound className="size-4 shrink-0 text-muted-foreground" />
        <span className="flex-1">
          {ssh.isPending ? translate("cloneDialog.tryingYourSSHKeys") : ssh.isError ? translate("cloneDialog.sSHKeySignInFailed") : translate("cloneDialog.signedInWithYourSSH")}
          {ssh.isError && <span className="mt-0.5 block text-[11px] text-muted-foreground">{errorMessage(ssh.error)}</span>}
        </span>
        <Button size="sm" onClick={() => ssh.mutate()} disabled={busy}>
          {t("cloneDialog.useSSHKey")}
        </Button>
      </div>
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          password_.mutate();
        }}
      >
        <p className="text-[12px] text-muted-foreground">{t("cloneDialog.signInHint", { host: account.host })}</p>
        <Field label={t("cloneDialog.email")}>
          <input value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="username" aria-label={t("cloneDialog.email")} className={inputClass} />
        </Field>
        <Field label={t("cloneDialog.password")}>
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" aria-label={t("cloneDialog.password")} className={inputClass} />
        </Field>
        <ErrorText error={password_.error} />
        <div className="flex justify-end">
          <Button type="submit" variant="primary" size="sm" disabled={busy || !email.trim() || !password}>
            {password_.isPending && <Loader2 className="animate-spin" />}
            {t("cloneDialog.signIn")}
          </Button>
        </div>
      </form>
    </div>
  );
}

function ProjectStep({ server, initialQuery = "", onPick, onSignIn }: { server: string; initialQuery?: string; onPick: (p: RemoteProject) => void; onSignIn: () => void }) {
  const { t } = useTranslation();
  const [query, setQuery] = useState(initialQuery);
  const projects = useQuery({ queryKey: ["remote", server, "projects"], queryFn: () => desktop.listProjects(server), retry: false });
  const groups = useMemo(() => {
    const q = query.trim().toLowerCase();
    const map = new Map<string, RemoteProject[]>();
    for (const p of projects.data ?? []) {
      if (q && ![p.project, p.name, p.org, p.orgName, p.description].some((v) => v.toLowerCase().includes(q))) continue;
      map.set(p.orgName || p.org, [...(map.get(p.orgName || p.org) ?? []), p]);
    }
    return [...map.entries()];
  }, [projects.data, query]);

  if (projects.isPending) {
    return (
      <p className="flex items-center gap-2 text-[12px] text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> {t("cloneDialog.loadingProjects")}
      </p>
    );
  }
  if (projects.isError) {
    return (
      <div className="space-y-3">
        <ErrorText error={projects.error} />
        <Button size="sm" onClick={onSignIn}>
          {t("cloneDialog.signInAgain")}
        </Button>
      </div>
    );
  }
  if (projects.data.length === 0) return <p className="text-[12px] text-muted-foreground">{t("cloneDialog.thisAccountCannotReadAny")}</p>;
  return (
    <div className="space-y-3">
      <label className="flex items-center gap-1.5 rounded-md border border-border bg-background px-2 py-1.5 text-[12px]">
        <Search className="size-3.5 text-muted-foreground" />
        <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("cloneDialog.filterProjects")} aria-label={t("cloneDialog.filterProjects")} className="flex-1 bg-transparent outline-none" />
      </label>
      {groups.length === 0 && <p className="text-[12px] text-muted-foreground">{t("cloneDialog.noProjectMatches")}</p>}
      {groups.map(([org, list]) => (
        <section key={org} aria-label={org}>
          <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{org}</h3>
          <ul className="space-y-0.5">
            {list.map((p) => (
              <li key={`${p.org}/${p.project}`}>
                <button type="button" onClick={() => onPick(p)} className="w-full rounded-md px-2 py-1.5 text-left hover:bg-muted">
                  <span className="block text-[13px] font-medium">{p.name || p.project}</span>
                  <span className="block truncate text-[11px] text-muted-foreground">
                    {p.org}/{p.project}
                    {p.description ? translate("cloneDialog.text2", { description: p.description }) : ""}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

type OptionsProps = { server: string; onSignIn: () => void; project: RemoteProject; cloning: boolean; onCloning: (busy: boolean) => void; onCloned: (repo: Repo) => void };

function OptionsStep({ server, onSignIn, project, cloning, onCloning, onCloned }: OptionsProps) {
  const { t } = useTranslation();
  const branches = useQuery({ queryKey: ["remote", server, project.org, project.project, "branches"], queryFn: () => desktop.remoteBranches(server, project.org, project.project) });
  const [branch, setBranch] = useState("");
  const chosen = branch || branches.data?.find((b) => b.isDefault)?.name || "";
  const [sparse, setSparse] = useState<string[] | null>(null);
  const [parent, setParent] = useState(() => readSetting(PARENT_KEY) ?? "");
  const [name, setName] = useState(project.project);
  const [progress, setProgress] = useState<OpProgress | undefined>();
  const [error, setError] = useState<unknown>(null);
  const call = useRef<Cancellable<Repo | null> | null>(null);
  const target = parent.trim() && name.trim() ? joinPath(parent.trim(), name.trim()) : "";

  useEffect(() => desktop.onOpProgress((p) => p.op === "clone" && p.root === target && setProgress(p)), [target]);

  const browse = async () => {
    try {
      const dir = await desktop.chooseFolder(translate("cloneDialog.whereShouldTheCloneGo"));
      if (dir) setParent(dir);
    } catch (err) {
      setError(err);
    }
  };

  const start = () => {
    setError(null);
    setProgress(undefined);
    writeSetting(PARENT_KEY, parent.trim());
    onCloning(true);
    const pending = desktop.clone({ server, org: project.org, project: project.project, branch: chosen, sparse: sparse ?? [], target });
    call.current = pending;
    pending.then(
      (repo) => {
        call.current = null;
        if (repo) onCloned(repo);
      },
      (err) => {
        call.current = null;
        setError(err);
        onCloning(false);
      },
    );
  };

  if (cloning) {
    const { label, percent } = describeProgress({ op: "clone", progress, cancelling: false });
    return (
      <div className="space-y-3" role="status" aria-live="polite">
        <p className="text-[12px]">
          {label} <span className="text-muted-foreground">{t("cloneDialog.into", { target })}</span>
        </p>
        <div className="h-1.5 overflow-hidden rounded-full bg-muted">
          <div className={percent === undefined ? "h-full w-1/3 animate-pulse rounded-full bg-accent" : "h-full rounded-full bg-accent"} style={percent === undefined ? undefined : { width: `${percent}%` }} />
        </div>
        <div className="flex justify-end">
          <Button size="sm" onClick={() => call.current?.cancel?.()}>
            {t("cloneDialog.cancel")}
          </Button>
        </div>
      </div>
    );
  }

  if (branches.isError || (branches.data && branches.data.length === 0)) {
    return (
      <div className="space-y-3">
        {branches.isError ? <ErrorText error={branches.error} /> : <p className="text-[12px] text-muted-foreground">{t("cloneDialog.thisProjectHasNoBranches")}</p>}
        <div className="flex gap-2">
          <Button size="sm" onClick={() => void branches.refetch()}>
            {t("cloneDialog.retry")}
          </Button>
          <Button size="sm" onClick={onSignIn}>
            {t("cloneDialog.signInAgain")}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <Field label={t("cloneDialog.branch")}>
        <select value={chosen} onChange={(e) => setBranch(e.target.value)} aria-label={t("cloneDialog.branch")} className={inputClass} disabled={!branches.data}>
          {(branches.data ?? []).map((b) => (
            <option key={b.name} value={b.name}>
              {b.name}
              {b.isDefault ? translate("cloneDialog.default") : ""}
            </option>
          ))}
        </select>
      </Field>
      <div className="space-y-1">
        <span className="text-[12px] font-medium">{t("cloneDialog.folders")}</span>
        <SparsePicker
          load={(path) => desktop.remoteTree(server, project.org, project.project, chosen, path)}
          cacheKey={["remote", server, project.org, project.project, chosen]}
          value={sparse}
          onChange={setSparse}
        />
      </div>
      <div className="grid grid-cols-[1fr_auto] items-end gap-2">
        <Field label={t("cloneDialog.location")}>
          <input value={parent} onChange={(e) => setParent(e.target.value)} placeholder={t("cloneDialog.homeYouProjects")} aria-label={t("cloneDialog.location")} className={inputClass} />
        </Field>
        <Button size="sm" onClick={() => void browse()}>
          <FolderOpen />
          {t("cloneDialog.browse")}
        </Button>
      </div>
      <Field label={t("cloneDialog.folderName")}>
        <input value={name} onChange={(e) => setName(e.target.value)} aria-label={t("cloneDialog.folderName")} className={inputClass} />
      </Field>
      {target && <p className="break-all font-mono text-[11px] text-muted-foreground">{target}</p>}
      <ErrorText error={error} />
      <div className="flex justify-end">
        <Button variant="primary" size="sm" onClick={start} disabled={!target || !chosen || (sparse !== null && sparse.length === 0)}>
          {t("cloneDialog.clone")}
        </Button>
      </div>
    </div>
  );
}
