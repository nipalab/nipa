import type { Commit, Tag } from "@/api/desktop";

const IMAGE_EXT = /\.(png|jpe?g|gif|bmp|tga|tiff?|webp|dds|ktx2|exr|hdr|psd|psb)$/i;

export const isImagePath = (path: string) => IMAGE_EXT.test(path);

export function commitTitle(message: string): string {
  return message.split("\n", 1)[0].trim() || "(no message)";
}

export function commitBody(message: string): string {
  const at = message.indexOf("\n");
  return at < 0 ? "" : message.slice(at + 1).trim();
}

export const shortId = (id: string) => id.slice(0, 10);

// filterCommits keeps the commits whose author, email, message or id
// contains every word of the query.
export function filterCommits(commits: Commit[], query: string): Commit[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return commits;
  return commits.filter((c) => {
    const text = `${c.author} ${c.email} ${c.message} ${c.id} ${c.hash}`.toLowerCase();
    return words.every((w) => text.includes(w));
  });
}

export function tagsByCommit(tags: Tag[]): Map<string, Tag[]> {
  const out = new Map<string, Tag[]>();
  for (const t of tags) out.set(t.commitId, [...(out.get(t.commitId) ?? []), t]);
  return out;
}
