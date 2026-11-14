
import { translate , locale } from "@/i18n";// timeAgo renders an RFC 3339 instant relative to now ("5 min ago").
export function timeAgo(iso: string, now: number = Date.now()): string {
  const at = Date.parse(iso);
  if (!iso || Number.isNaN(at)) return "";
  const s = Math.max(0, Math.round((now - at) / 1000));
  if (s < 45) return translate("time.justNow");
  const m = Math.round(s / 60);
  if (m < 60) return translate("time.minAgo", { m });
  const h = Math.round(m / 60);
  if (h < 24) return translate("time.hAgo", { h });
  const d = Math.round(h / 24);
  if (d < 30) return translate("time.days", { count: d });
  return new Date(at).toLocaleDateString(locale());
}
