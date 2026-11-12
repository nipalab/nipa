import { Suspense, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, History as HistoryIcon, Image as ImageIcon, Loader2, Lock, RotateCcw } from "lucide-react";
import { desktop, type Commit, type FileLock } from "@/api/desktop";
import { viewerFor } from "@/components/changes/DiffViewer";
import { FileIcon, StatusLetter } from "@/components/changes/FileIcon";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm";
import { formatBytes } from "@/lib/diff";
import { kindName, lockFor, statusName, type Row } from "@/lib/files";
import { commitTitle, isImagePath, shortId } from "@/lib/history";
import { timeAgo } from "@/lib/time";
import { errorMessage } from "@/lib/utils";
import { ActionBar, type FileAction } from "./FileMenu";

const VERSIONS = 20;

type DetailProps = {
  root: string;
  branch: string;
  row: Row;
  locks: FileLock[];
  actions: FileAction[];
  busy: boolean;
  onRestore: (commit: Commit) => void;
  onShowCommit: (id: string) => void;
};

export function FileDetail({ root, branch, row, locks, actions, busy, onRestore, onShowCommit }: DetailProps) {
  const info = useQuery({ queryKey: ["file", root, row.path, "info", branch], queryFn: () => desktop.fileInfo(root, branch, row.path), retry: false });
  const onDisk = row.status !== "missing" && row.status !== "deleted";
  const view = useQuery({ queryKey: ["file", root, row.path, "view"], queryFn: () => desktop.fileView(root, row.path), enabled: onDisk, retry: false });
  const [confirm, setConfirm] = useState<Commit | null>(null);
  const lock = lockFor(row.path, locks);
  const last = info.data?.lastCommit;

  const restore = (commit: Commit) => {
    if (row.status) setConfirm(commit);
    else onRestore(commit);
  };

  return (
    <div className="flex h-full min-w-0 flex-col">
      <header className="shrink-0 space-y-2 border-b border-border px-4 py-2.5">
        <div className="flex items-center gap-2">
          <FileIcon kind={row.kind} />
          <h2 className="min-w-0 flex-1 truncate text-[13px] font-semibold" title={row.path}>
            {row.name}
          </h2>
          {row.status && <StatusLetter status={row.status} />}
        </div>
        <ActionBar actions={actions} />
      </header>
      <div className="h-[45%] min-h-48 shrink-0 border-b border-border">
        {!onDisk ? (
          <Centered>{row.status === "missing" ? "The file is missing from disk. Get a version below or discard the change." : "The file is deleted in this clone."}</Centered>
        ) : view.isPending ? (
          <Centered>
            <Loader2 className="size-4 animate-spin" /> Loading…
          </Centered>
        ) : view.isError ? (
          <Centered>
            <AlertTriangle className="size-4 text-danger" /> {errorMessage(view.error)}
          </Centered>
        ) : view.data ? (
          <Viewer root={root} view={view.data} />
        ) : (
          <Centered>The file is not on disk.</Centered>
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 px-4 py-3 text-[12px]">
          <Term label="Path">
            <span className="select-text break-all font-mono text-[11px]">{row.path}</span>
          </Term>
          <Term label="Kind">{kindName(row)}</Term>
          <Term label="Size">{formatBytes(row.size)}</Term>
          <Term label="Status">{statusName(row.status)}</Term>
          <Term label="Lock">
            {lock ? (
              <span className="flex items-center gap-1">
                <Lock className="size-3" />
                {lock.mine ? "Locked by you" : `Locked by ${lock.heldByName || lock.heldBy}`}
                {lock.path !== row.path && <span className="text-muted-foreground">(folder {lock.path})</span>}
              </span>
            ) : (
              <span className="text-muted-foreground">Not locked</span>
            )}
          </Term>
          <Term label="Last change">
            {info.isPending ? (
              <span className="text-muted-foreground">Loading…</span>
            ) : last ? (
              <span className="min-w-0">
                <button type="button" className="text-left hover:underline" onClick={() => onShowCommit(last.id)} title="Show this commit in History">
                  {commitTitle(last.message)}
                </button>
                <span className="block text-[11px] text-muted-foreground">
                  {last.author || last.email} · {timeAgo(last.date)} · <span className="font-mono">{shortId(last.id)}</span>
                </span>
              </span>
            ) : (
              <span className="text-muted-foreground">{info.data?.lastCommitError ? "Not available offline" : row.tracked ? "Unknown" : "Not pushed yet"}</span>
            )}
          </Term>
        </dl>
        {row.tracked && <Versions root={root} branch={branch} path={row.path} busy={busy} onRestore={restore} onShowCommit={onShowCommit} />}
      </div>
      {confirm && (
        <ConfirmDialog
          title="Replace your changes?"
          message={`${row.name} has changes that are not pushed. Getting the version from ${shortId(confirm.id)} "${commitTitle(confirm.message)}" overwrites them.`}
          confirmLabel="Get this version"
          onCancel={() => setConfirm(null)}
          onConfirm={() => {
            const commit = confirm;
            setConfirm(null);
            onRestore(commit);
          }}
        />
      )}
    </div>
  );
}

function Viewer({ root, view }: { root: string; view: NonNullable<Awaited<ReturnType<typeof desktop.fileView>>> }) {
  const Component = viewerFor(view);
  return (
    <Suspense fallback={null}>
      <Component root={root} diff={view} layout="unified" />
    </Suspense>
  );
}

function Versions({ root, branch, path, busy, onRestore, onShowCommit }: { root: string; branch: string; path: string; busy: boolean; onRestore: (c: Commit) => void; onShowCommit: (id: string) => void }) {
  const versions = useQuery({ queryKey: ["file", root, path, "versions", branch], queryFn: () => desktop.history(root, { branch, path, limit: VERSIONS }), retry: false });
  const image = isImagePath(path);
  return (
    <section aria-label="Versions" className="border-t border-border px-4 py-3">
      <h3 className="mb-2 flex items-center gap-1.5 text-[12px] font-semibold">
        <HistoryIcon className="size-3.5" />
        Versions
      </h3>
      {versions.isPending ? (
        <p className="text-[11px] text-muted-foreground">Loading versions…</p>
      ) : versions.isError ? (
        <p className="text-[11px] text-muted-foreground">Versions are not available: {errorMessage(versions.error)}</p>
      ) : versions.data.commits.length === 0 ? (
        <p className="text-[11px] text-muted-foreground">No pushed versions yet.</p>
      ) : (
        <ol className="space-y-1">
          {versions.data.commits.map((c, i) => (
            <li key={c.id} className="flex items-center gap-2 rounded-md px-1 py-1 hover:bg-muted">
              {image && <VersionThumb root={root} path={path} commit={c.id} />}
              <button type="button" className="min-w-0 flex-1 text-left" onClick={() => onShowCommit(c.id)} title="Show this commit in History">
                <span className="block truncate text-[12px]">{commitTitle(c.message)}</span>
                <span className="block truncate text-[11px] text-muted-foreground">
                  {c.author || c.email} · {timeAgo(c.date)} · <span className="font-mono">{shortId(c.id)}</span>
                  {i === 0 && " · latest"}
                </span>
              </button>
              <Button size="sm" disabled={busy} onClick={() => onRestore(c)} title="Write this version over the file in your clone">
                <RotateCcw />
                Get this version
              </Button>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

function VersionThumb({ root, path, commit }: { root: string; path: string; commit: string }) {
  const thumb = useQuery({
    queryKey: ["commit", root, commit, path, "thumb"],
    queryFn: () => desktop.imageDiff(root, null, { source: "revision", revision: commit, path }, { maxSize: 128 }),
    staleTime: Infinity,
    retry: false,
  });
  const info = thumb.data?.new;
  return (
    <span className="checkerboard flex size-9 shrink-0 items-center justify-center overflow-hidden rounded">
      {info && !info.hdr && info.src ? <img src={info.src} alt={`Version ${shortId(commit)}`} className="max-h-full max-w-full object-contain" /> : <ImageIcon className="size-3.5 text-muted-foreground" />}
    </span>
  );
}

function Term({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </>
  );
}

function Centered({ children }: { children: ReactNode }) {
  return <div className="flex h-full items-center justify-center gap-2 p-6 text-center text-xs text-muted-foreground">{children}</div>;
}
