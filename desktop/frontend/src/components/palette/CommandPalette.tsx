import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import {
  ArrowDownToLine,
  ArrowLeft,
  ArrowUpFromLine,
  GitBranch,
  GitCommitHorizontal,
  GitPullRequest,
  GitPullRequestCreate,
  Keyboard,
  Loader2,
  Lock,
  RefreshCw,
  Search,
  Settings,
  Tag,
  type LucideIcon,
} from "lucide-react";
import { desktop, type Repo } from "@/api/desktop";
import { FileIcon } from "@/components/changes/FileIcon";
import { labels, useSimple } from "@/lib/mode";
import { hasOpenConflicts, visibleTabs, type Intent } from "@/components/tabs";
import { useDebounced } from "@/hooks/useDebounced";
import { fuzzyMatch, rank, segments } from "@/lib/fuzzy";
import { commitTitle, shortId } from "@/lib/history";
import { commitMatches, looksLikeCommitId, mergeFileHits, mrNumber } from "@/lib/palette";
import { chordMatches, shortcut, shortcutHint } from "@/lib/shortcuts";
import { cn } from "@/lib/utils";

export type PaletteMode = "all" | "switch" | "lock";
export type Go = (root: string, intent: Omit<Intent, "seq">) => void;

type Section = "Actions" | "Files" | "Branches and tags" | "Merge requests" | "Commits";
const sectionOrder: Section[] = ["Actions", "Files", "Branches and tags", "Merge requests", "Commits"];

type Item = {
  key: string;
  section: Section;
  icon: ReactNode;
  title: string;
  hits?: number[];
  dimUntil?: number;
  detail?: string;
  hint?: string;
  run: () => void;
};

const DEBOUNCE_MS = 150;
const FILE_LIMIT = 10;
const LOCK_FILE_LIMIT = 30;
const codePoints = (s: string) => Array.from(s).length;
const icon = (Icon: LucideIcon) => <Icon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />;

type PaletteProps = {
  repos: Repo[];
  current?: Repo;
  initialMode?: PaletteMode;
  onClose: () => void;
  onGo: Go;
  onSettings: () => void;
  onShortcuts: () => void;
};

export function CommandPalette({ repos, current, initialMode = "all", onClose, onGo, onSettings, onShortcuts }: PaletteProps) {
  const simple = useSimple();
  const [mode, setMode] = useState<PaletteMode>(current ? initialMode : "all");
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => input.current?.focus(), [mode]);
  const q = query.trim();
  const dq = useDebounced(q, DEBOUNCE_MS);
  const root = current && !current.error ? current.root : "";
  const usable = useMemo(() => repos.filter((r) => !r.error), [repos]);
  const searchIn = mode === "lock" ? usable.filter((r) => r.root === root) : mode === "all" ? usable : [];

  const fileSearches = useQueries({
    queries: searchIn.map((r) => ({
      queryKey: ["search-files", r.root, dq, mode === "lock" ? LOCK_FILE_LIMIT : FILE_LIMIT],
      queryFn: () => desktop.searchFiles(r.root, dq, mode === "lock" ? LOCK_FILE_LIMIT : FILE_LIMIT),
      enabled: dq !== "",
      staleTime: 10_000,
    })),
  });
  const wantsRefs = !!root && !simple && (mode === "switch" || (mode === "all" && q !== ""));
  const branches = useQuery({ queryKey: ["branches", root], queryFn: () => desktop.branches(root), enabled: wantsRefs });
  const tagList = useQuery({ queryKey: ["tags", root], queryFn: () => desktop.tags(root), enabled: wantsRefs && mode === "all" });
  const searching = mode === "all" && !!root && dq !== "";
  const searchingMrs = searching && !simple;
  const mrs = useQuery({
    queryKey: ["mrs", root, "palette", dq],
    queryFn: () => desktop.mergeRequests(root, { status: "", search: dq }),
    enabled: searchingMrs && mrNumber(dq) === null,
    staleTime: 10_000,
  });
  const branch = current?.branch ?? "";
  const changes = useQuery({ queryKey: ["changes", root], queryFn: () => desktop.changes(root), enabled: !!root && mode === "all" });
  const log = useQuery({
    queryKey: ["history", root, "palette", branch],
    queryFn: () => desktop.history(root, { branch, limit: 50 }),
    enabled: mode === "all" && !!root && q !== "",
    staleTime: 30_000,
  });
  const loadedCommits = log.data?.commits ?? [];
  const commitLookup = searching && looksLikeCommitId(dq) && !loadedCommits.some((c) => c.id === dq);
  const lookup = useQuery({
    queryKey: ["history", root, "commit", dq],
    queryFn: () => desktop.history(root, { branch, start: dq, limit: 1 }),
    enabled: commitLookup,
    staleTime: Infinity,
    retry: false,
  });

  const go = (target: string, intent: Omit<Intent, "seq">) => {
    onClose();
    onGo(target, intent);
  };
  const enter = (next: PaletteMode) => {
    setMode(next);
    setQuery("");
    setActive(0);
  };
  const repoName = (r: string) => repos.find((x) => x.root === r)?.name ?? r;

  const items = ((): Item[] => {
    const out: Item[] = [];
    if (mode === "switch") {
      const list = branches.data ?? [];
      const ranked = q ? rank(list, q, (b) => b.name) : list.map((item) => ({ item, match: undefined }));
      for (const { item: b, match } of ranked) {
        const here = b.name === branch;
        out.push({
          key: `branch:${b.name}`,
          section: "Branches and tags",
          icon: icon(GitBranch),
          title: b.name,
          hits: match?.positions,
          detail: here ? "Current branch" : b.default ? "Default branch" : undefined,
          hint: here ? undefined : "Switch",
          run: () => go(root, here ? { tab: "branches" } : { tab: "branches", action: "switch", target: b.name }),
        });
      }
      return out;
    }
    if (mode === "lock") {
      const hits = mergeFileHits(fileSearches.map((s, i) => ({ root: searchIn[i]?.root ?? "", hits: s.data?.hits ?? [] })), dq, LOCK_FILE_LIMIT);
      for (const { hit, match } of hits) {
        out.push({
          key: `lock:${hit.path}`,
          section: "Files",
          icon: <FileIcon kind={hit.kind} />,
          title: hit.path,
          hits: match.positions,
          dimUntil: codePoints(hit.path) - codePoints(hit.name),
          hint: "Lock",
          run: () => go(root, { tab: "locks", action: "lock", path: hit.path }),
        });
      }
      return out;
    }

    const actions: Omit<Item, "section">[] = [];
    if (root) {
      actions.push(
        { key: "update", icon: icon(ArrowDownToLine), title: labels(simple).update, detail: "Get the latest version of the branch", hint: shortcutHint("update"), run: () => go(root, { tab: "", action: "update" }) },
        { key: "push", icon: icon(ArrowUpFromLine), title: `${labels(simple).push}…`, detail: `Write the ${simple ? "submit" : "push"} message`, hint: shortcutHint("focusMessage"), run: () => go(root, { tab: "changes", action: "focus-message" }) },
        ...(simple
          ? []
          : [
              { key: "switch", icon: icon(GitBranch), title: "Switch branch…", run: () => enter("switch") },
              { key: "new-mr", icon: icon(GitPullRequestCreate), title: "New merge request", run: () => go(root, { tab: "mrs", action: "new-mr" }) },
            ]),
        { key: "lock", icon: icon(Lock), title: "Lock file…", run: () => enter("lock") },
        { key: "refresh", icon: icon(RefreshCw), title: "Refresh", hint: shortcutHint("refresh"), run: () => go(root, { tab: "", action: "refresh" }) },
      );
      visibleTabs(hasOpenConflicts(changes.data), simple)
        .forEach((t, i) =>
          actions.push({ key: `tab:${t.id}`, icon: icon(t.Icon), title: `Go to ${t.label}`, hint: i < 9 ? shortcutHint("tab").replace("1–9", String(i + 1)) : undefined, run: () => go(root, { tab: t.id }) }),
        );
    }
    actions.push(
      { key: "settings", icon: icon(Settings), title: "Settings", hint: shortcutHint("settings"), run: () => {
          onClose();
          onSettings();
        } },
      { key: "shortcuts", icon: icon(Keyboard), title: "Keyboard shortcuts", hint: shortcutHint("help"), run: () => {
          onClose();
          onShortcuts();
        } },
    );
    const shownActions = q ? rank(actions, q, (a) => a.title, 6).map(({ item, match }) => ({ ...item, hits: match.positions })) : actions;
    for (const a of shownActions) out.push({ ...a, section: "Actions" });
    if (!q) return out;

    const multi = searchIn.length > 1;
    const hits = mergeFileHits(fileSearches.map((s, i) => ({ root: searchIn[i]?.root ?? "", hits: s.data?.hits ?? [] })), dq, FILE_LIMIT);
    for (const { root: r, hit, match } of hits) {
      out.push({
        key: `file:${r}:${hit.path}`,
        section: "Files",
        icon: <FileIcon kind={hit.kind} />,
        title: hit.path,
        hits: match.positions,
        dimUntil: codePoints(hit.path) - codePoints(hit.name),
        detail: multi ? repoName(r) : undefined,
        run: () => go(r, { tab: "files", path: hit.path }),
      });
    }

    if (root) {
      const refs = simple ? [] : [
        ...(branches.data ?? []).map((b) => ({ kind: "branch" as const, name: b.name })),
        ...(tagList.data ?? []).map((t) => ({ kind: "tag" as const, name: t.name })),
      ];
      for (const { item: ref, match } of rank(refs, q, (r) => r.name, 6)) {
        const here = ref.kind === "branch" && ref.name === branch;
        out.push({
          key: `${ref.kind}:${ref.name}`,
          section: "Branches and tags",
          icon: icon(ref.kind === "branch" ? GitBranch : Tag),
          title: ref.name,
          hits: match.positions,
          detail: here ? "Current branch" : ref.kind === "tag" ? "Tag" : undefined,
          hint: here ? undefined : ref.kind === "tag" ? "Check out" : "Switch",
          run: () =>
            go(root, here ? { tab: "branches" } : ref.kind === "tag" ? { tab: "branches", action: "checkout-tag", target: ref.name } : { tab: "branches", action: "switch", target: ref.name }),
        });
      }

      const number = simple ? null : mrNumber(q);
      if (number !== null) {
        out.push({ key: `mr:${number}`, section: "Merge requests", icon: icon(GitPullRequest), title: `Open merge request #${number}`, run: () => go(root, { tab: "mrs", number }) });
      } else {
        for (const mr of simple ? [] : (mrs.data?.items ?? []).slice(0, 5)) {
          out.push({
            key: `mr:${mr.number}`,
            section: "Merge requests",
            icon: icon(GitPullRequest),
            title: `#${mr.number} ${mr.title}`,
            hits: fuzzyMatch(q, `#${mr.number} ${mr.title}`)?.positions,
            detail: `${mr.source} → ${mr.target} · ${mr.draft ? "draft" : mr.status}`,
            run: () => go(root, { tab: "mrs", number: mr.number }),
          });
        }
      }

      const commits = commitMatches(loadedCommits, q, 5);
      const found = lookup.data?.commits[0];
      if (commitLookup && found?.id === dq && !commits.some((c) => c.id === found.id)) commits.unshift(found);
      for (const c of commits) {
        out.push({
          key: `commit:${c.id}`,
          section: "Commits",
          icon: icon(GitCommitHorizontal),
          title: commitTitle(c.message),
          detail: `${shortId(c.id)} · ${c.author}`,
          run: () => go(root, { tab: "history", commit: c.id }),
        });
      }
    }
    return out.sort((a, b) => sectionOrder.indexOf(a.section) - sectionOrder.indexOf(b.section));
  })();

  useEffect(() => setActive(0), [mode, q]);
  const selected = Math.min(active, Math.max(0, items.length - 1));
  const listId = "palette-list";
  const optionId = (i: number) => `palette-option-${i}`;
  useEffect(() => {
    document.getElementById(optionId(selected))?.scrollIntoView?.({ block: "nearest" });
  }, [selected]);

  const loading = fileSearches.some((s) => s.isFetching) || (mode === "switch" && branches.isPending) || q !== dq;
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    e.stopPropagation();
    if (chordMatches(shortcut("palette").chords[0], e.nativeEvent)) {
      e.preventDefault();
      onClose();
      return;
    }
    if (e.nativeEvent.isComposing) return;
    switch (e.key) {
      case "ArrowDown":
      case "ArrowUp": {
        e.preventDefault();
        if (items.length === 0) return;
        const step = e.key === "ArrowDown" ? 1 : -1;
        setActive((selected + step + items.length) % items.length);
        return;
      }
      case "Enter":
        e.preventDefault();
        items[selected]?.run();
        return;
      case "Escape":
        e.preventDefault();
        if (mode !== "all") enter("all");
        else onClose();
        return;
      case "Backspace":
        if (query === "" && mode !== "all") {
          e.preventDefault();
          enter("all");
        }
    }
  };

  const placeholder =
    mode === "switch" ? "Switch to branch…" : mode === "lock" ? "Find the file to lock…" : root ? "Search files, branches, merge requests, commits and actions" : "Search files and actions";
  const empty = mode === "lock" && !q ? "Type part of the file's name" : loading ? "Searching…" : "No results";

  let lastSection: Section | null = null;
  return (
    <div role="presentation" className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 p-4 pt-[12vh]" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        onMouseDown={(e) => e.target !== input.current && e.preventDefault()}
        className="flex max-h-[70vh] w-full max-w-xl flex-col overflow-hidden rounded-lg border border-border bg-card shadow-2xl">
        <div className="flex items-center gap-2 border-b border-border px-3">
          {mode !== "all" ? (
            <button type="button" onClick={() => enter("all")} aria-label="Back to all commands" className="text-muted-foreground hover:text-foreground">
              <ArrowLeft className="size-4" />
            </button>
          ) : (
            <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden />
          )}
          <input
            ref={input}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder={placeholder}
            role="combobox"
            aria-label="Command"
            aria-expanded="true"
            aria-controls={listId}
            aria-autocomplete="list"
            aria-activedescendant={items.length > 0 ? optionId(selected) : undefined}
            spellCheck={false}
            className="h-11 min-w-0 flex-1 bg-transparent text-[13px] outline-none placeholder:text-muted-foreground"
          />
          {loading && q !== "" && <Loader2 className="size-3.5 animate-spin text-muted-foreground" aria-label="Searching" />}
        </div>
        <div id={listId} role="listbox" aria-label="Results" className="min-h-0 flex-1 overflow-y-auto py-1">
          {items.length === 0 && <p className="px-4 py-6 text-center text-xs text-muted-foreground">{empty}</p>}
          {items.map((item, i) => {
            const header = item.section !== lastSection && mode === "all";
            lastSection = item.section;
            return (
              <div key={item.key}>
                {header && (
                  <div role="presentation" className="px-3 pb-1 pt-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                    {item.section}
                  </div>
                )}
                <div
                  id={optionId(i)}
                  role="option"
                  tabIndex={-1}
                  aria-selected={i === selected}
                  onMouseMove={() => i !== selected && setActive(i)}
                  onClick={item.run}
                  className={cn("mx-1 flex cursor-default items-center gap-2.5 rounded-md px-2 py-1.5 text-[12px]", i === selected && "bg-accent-soft")}
                >
                  {item.icon}
                  <span className="min-w-0 flex-1 truncate">
                    <Highlighted text={item.title} hits={item.hits} dimUntil={item.dimUntil} />
                    {item.detail && <span className="ml-2 text-[11px] text-muted-foreground">{item.detail}</span>}
                  </span>
                  {item.hint && <kbd className="shrink-0 rounded border border-border px-1 font-sans text-[10px] text-muted-foreground">{item.hint}</kbd>}
                </div>
              </div>
            );
          })}
        </div>
        <div className="flex gap-3 border-t border-border px-3 py-1.5 text-[10px] text-muted-foreground">
          <span>↑↓ to move</span>
          <span>Enter to choose</span>
          <span>Esc to {mode === "all" ? "close" : "go back"}</span>
        </div>
      </div>
    </div>
  );
}

function Highlighted({ text, hits, dimUntil = 0 }: { text: string; hits?: number[]; dimUntil?: number }) {
  let at = 0;
  return (
    <>
      {segments(text, hits).flatMap((seg, i) => {
        const start = at;
        at += Array.from(seg.text).length;
        const parts: { text: string; dim: boolean }[] = [];
        if (start < dimUntil && at > dimUntil) {
          const chars = Array.from(seg.text);
          parts.push({ text: chars.slice(0, dimUntil - start).join(""), dim: true }, { text: chars.slice(dimUntil - start).join(""), dim: false });
        } else parts.push({ text: seg.text, dim: start < dimUntil });
        return parts.map((p, j) => (
          <span key={`${i}-${j}`} className={cn(p.dim && "text-muted-foreground", seg.hit && "font-semibold text-accent")}>
            {p.text}
          </span>
        ));
      })}
    </>
  );
}

