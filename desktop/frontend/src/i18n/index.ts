import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { flushSettings, readSetting, writeSetting } from "@/lib/settings";
import en from "./locales/en.json";
import id from "./locales/id.json";
import ja from "./locales/ja.json";
import ko from "./locales/ko.json";
import zhCN from "./locales/zh-CN.json";

export const LANGUAGES = ["en", "id", "ja", "zh-CN", "ko"] as const;
export type Language = (typeof LANGUAGES)[number];
export type LanguageSetting = Language | "system";

export const LANGUAGE_KEY = "nipa.language";
export const LANGUAGE_NAMES: Record<Language, string> = {
  en: "English",
  id: "Bahasa Indonesia",
  ja: "日本語",
  "zh-CN": "简体中文",
  ko: "한국어",
};

export function fromLocale(locale: string | undefined): Language {
  const l = (locale ?? "").toLowerCase();
  if (l.startsWith("id") || l.startsWith("in")) return "id";
  if (l.startsWith("ja")) return "ja";
  if (l.startsWith("ko")) return "ko";
  if (l.startsWith("zh") && !/hant|-tw|-hk|-mo/.test(l)) return "zh-CN";
  return "en";
}

export function storedLanguage(): LanguageSetting {
  const v = readSetting(LANGUAGE_KEY);
  return (LANGUAGES as readonly string[]).includes(v ?? "") ? (v as Language) : "system";
}

export function resolveLanguage(setting: LanguageSetting, locale = typeof navigator === "undefined" ? "en" : navigator.language): Language {
  return setting === "system" ? fromLocale(locale) : setting;
}

void i18n.use(initReactI18next).init({
  resources: {
    en: { translation: en },
    id: { translation: id },
    ja: { translation: ja },
    "zh-CN": { translation: zhCN },
    ko: { translation: ko },
  },
  lng: resolveLanguage(storedLanguage()),
  fallbackLng: "en",
  keySeparator: false,
  nsSeparator: false,
  interpolation: { escapeValue: false },
  returnNull: false,
});

if (typeof document !== "undefined") document.documentElement.lang = i18n.language;

export const currentLanguage = (): Language => resolveLanguage(storedLanguage());

// changeLanguage stores the choice and reloads, so every label built at load
// time is rebuilt in the new language.
export async function changeLanguage(setting: LanguageSetting, reload: () => void = () => window.location.reload()) {
  writeSetting(LANGUAGE_KEY, setting === "system" ? null : setting);
  await flushSettings();
  reload();
}

export default i18n;

export const translate = (key: string, options?: Record<string, unknown>): string => i18n.t(key, options) as string;

export const plural = (n: number, noun: string): string => translate(`noun.${noun}`, { count: n });

export const locale = (): string => i18n.language || "en";
