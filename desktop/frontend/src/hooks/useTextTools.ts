import { createContext, useContext, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { desktop, type FileDiff } from "@/api/desktop";
import type { LineTools } from "@/components/changes/TextDiff";
import { blameCells, canBlame } from "@/lib/blame";
import { readSetting, writeSetting } from "@/lib/utils";

// RepoNav lets viewers deep inside a tab reach the repository view: show a
// commit in History or open a clone file at a line.
export type RepoNav = {
  showCommit?: (id: string) => void;
  openAt?: (path: string, line: number) => void;
};

export const RepoNavContext = createContext<RepoNav>({});

export const BLAME_KEY = "nipa.blame";

// useTextTools wires the text viewer's blame gutter and "Open in editor"
// for one file: blame reads the new side (the working file, or the
// revision of a commit diff); opening needs the file on disk.
export type TextTools = LineTools & { available: boolean; on: boolean; toggle: () => void; loading: boolean; error: unknown };

export function useTextTools(root: string, diff: FileDiff, text: boolean): TextTools {
  const nav = useContext(RepoNavContext);
  const [on, setOn] = useState(() => readSetting(BLAME_KEY) === "on");
  const available = text && canBlame(diff);
  const revision = diff.newRevision ?? "";
  const query = useQuery({
    queryKey: ["blame", root, diff.path, revision, diff.new?.size ?? 0, diff.additions, diff.deletions],
    queryFn: () => desktop.blame(root, diff.path, revision),
    enabled: available && on,
    retry: false,
    staleTime: 30_000,
  });
  const cells = useMemo(() => (query.data ? blameCells(query.data) : undefined), [query.data]);
  const toggle = () => {
    writeSetting(BLAME_KEY, on ? null : "on");
    setOn(!on);
  };
  const openAt = nav.openAt;
  const onDisk = text && !revision && !!diff.new && diff.status !== "deleted";
  return {
    available,
    on: available && on,
    toggle,
    loading: available && on && query.isFetching,
    error: available && on && query.isError ? query.error : null,
    blame: available && on ? cells : undefined,
    onSelectCommit: nav.showCommit,
    onOpenLine: openAt && onDisk ? (line: number) => openAt(diff.path, line) : undefined,
  };
}

