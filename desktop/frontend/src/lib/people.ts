// initials turns a display name into one or two letters for an avatar.
export function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return "?";
  const first = [...words[0]][0];
  if (words.length === 1) return first.toUpperCase();
  return (first + [...words[words.length - 1]][0]).toUpperCase();
}

// hueFor picks a stable hue from a name or id, so a person keeps their colour.
export function hueFor(key: string): number {
  let h = 0;
  for (const ch of key) h = (h * 31 + ch.codePointAt(0)!) % 360;
  return h;
}
