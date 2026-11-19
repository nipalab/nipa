const SYSTEM_NAMES: Record<string, string> = {
  git: "Git",
  hg: "Mercurial",
  svn: "SVN",
  p4: "Perforce",
};

function splitOrigin(origin?: string): { system: string; id: string } | null {
  if (!origin) return null;
  const parts = origin.split(":");
  if (parts.length < 3 || !parts[0] || !parts[2]) return null;
  return { system: parts[0], id: parts[2] };
}

export function originSystemName(origin?: string): string {
  const parsed = splitOrigin(origin);
  if (!parsed) return "";
  return SYSTEM_NAMES[parsed.system] ?? parsed.system;
}

export function originShortId(origin?: string): string {
  const parsed = splitOrigin(origin);
  if (!parsed) return "";
  switch (parsed.system) {
    case "git":
    case "hg":
      return parsed.id.slice(0, 7);
    case "svn":
      return `r${parsed.id}`;
    case "p4":
      return `CL ${parsed.id}`;
    default:
      return parsed.id;
  }
}
