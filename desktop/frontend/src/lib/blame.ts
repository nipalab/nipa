import type { Blame, Commit, FileDiff } from "@/api/desktop";
import { commitTitle, shortId } from "@/lib/history";
import { originShortId, originSystemName } from "@/lib/origin";
import { timeAgo } from "@/lib/time";
import { translate , locale } from "@/i18n";

// BlameCell is what the gutter shows next to one line. first marks the start
// of a run of lines from the same commit, where the author and age are shown.
export type BlameCell = {
  commit?: Commit;
  uncommitted: boolean;
  first: boolean;
  label: string;
  title: string;
};

export type BlameCells = Map<number, BlameCell>;

// canBlame tells whether a diff's new side is a text file blame can read.
export const canBlame = (diff: FileDiff): boolean => !!diff.new && !diff.new.binary && diff.status !== "deleted" && !diff.contentUnavailable;

// blameCells keys every blamed line by its 1-based line number.
export function blameCells(blame: Blame, now: number = Date.now()): BlameCells {
  const commits = new Map(blame.commits.map((c) => [c.id, c]));
  const out: BlameCells = new Map();
  let previous = "";
  blame.lines.forEach((line, i) => {
    const owner = line.uncommitted ? "+" : line.commitId || "?";
    const first = owner !== previous;
    previous = owner;
    const commit = line.commitId ? commits.get(line.commitId) : undefined;
    let label = "";
    let title: string;
    if (line.uncommitted) {
      label = translate("blame.notPushed");
      title = translate("blame.changedInYourCloneNot");
    } else if (commit) {
      label = translate("blame.text", { email: commit.author || commit.email, value: timeAgo(commit.date, now) });
      title = translate("blame.text2", { value: shortId(commit.id), value2: commitTitle(commit.message), email: commit.author || commit.email, toLocaleString: new Date(commit.date).toLocaleString(locale()) });
      const system = originSystemName(commit.origin);
      if (system) title += `\n${translate("history.importedFrom", { system })} ${originShortId(commit.origin)}`;
    } else {
      label = translate("blame.older");
      title = translate("blame.olderThanTheHistoryNipa");
    }
    out.set(i + 1, {
      commit,
      uncommitted: line.uncommitted,
      first,
      label: first ? label : "",
      title,
    });
  });
  return out;
}
