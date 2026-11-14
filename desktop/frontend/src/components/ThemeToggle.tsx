import { Monitor, Moon, Sun } from "lucide-react";
import { useTheme, type Theme } from "@/lib/theme";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { translate } from "@/i18n";

const options: { value: Theme; label: string; Icon: typeof Sun }[] = [
  { value: "light", label: translate("themeToggle.light"), Icon: Sun },
  { value: "dark", label: translate("themeToggle.dark"), Icon: Moon },
  { value: "system", label: translate("themeToggle.system"), Icon: Monitor },
];

export function ThemeToggle() {
  const { t } = useTranslation();
  const { theme, setTheme } = useTheme();
  return (
    <div role="radiogroup" aria-label={t("themeToggle.theme")} className="no-drag flex rounded-md border border-border bg-card p-0.5">
      {options.map(({ value, label, Icon }) => (
        <button
          key={value}
          type="button"
          role="radio"
          aria-checked={theme === value}
          aria-label={label}
          title={label}
          onClick={() => setTheme(value)}
          className={cn(
            "flex size-6 items-center justify-center rounded text-muted-foreground transition-colors hover:text-foreground",
            theme === value && "bg-muted text-foreground",
          )}
        >
          <Icon className="size-3.5" />
        </button>
      ))}
    </div>
  );
}
