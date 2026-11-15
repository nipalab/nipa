import { afterEach, describe, expect, it, vi } from "vitest";
import { resetSettingsForTest, writeSetting } from "@/lib/settings";
import i18n, { changeLanguage, currentLanguage, fromLocale, LANGUAGE_KEY, plural, resolveLanguage, storedLanguage, translate } from "./index";

afterEach(async () => {
  resetSettingsForTest();
  localStorage.clear();
  await i18n.changeLanguage("en");
});

describe("language choice", () => {
  it("maps locales to the supported languages", () => {
    expect(fromLocale("ja-JP")).toBe("ja");
    expect(fromLocale("ko")).toBe("ko");
    expect(fromLocale("id-ID")).toBe("id");
    expect(fromLocale("in")).toBe("id");
    expect(fromLocale("zh-CN")).toBe("zh-CN");
    expect(fromLocale("zh-Hans")).toBe("zh-CN");
    expect(fromLocale("zh-TW")).toBe("en");
    expect(fromLocale("zh-Hant")).toBe("en");
    expect(fromLocale("de-DE")).toBe("en");
    expect(fromLocale(undefined)).toBe("en");
  });

  it("follows the system unless a language is chosen", () => {
    expect(resolveLanguage("system", "ko-KR")).toBe("ko");
    expect(resolveLanguage("ja", "ko-KR")).toBe("ja");
    expect(storedLanguage()).toBe("system");
    writeSetting(LANGUAGE_KEY, "zh-CN");
    expect(storedLanguage()).toBe("zh-CN");
    expect(currentLanguage()).toBe("zh-CN");
    writeSetting(LANGUAGE_KEY, "fr");
    expect(storedLanguage()).toBe("system");
  });

  it("stores the choice and reloads", async () => {
    const reload = vi.fn();
    await changeLanguage("ja", reload);
    expect(storedLanguage()).toBe("ja");
    expect(reload).toHaveBeenCalledOnce();
    await changeLanguage("system", reload);
    expect(storedLanguage()).toBe("system");
  });
});

describe("translation", () => {
  it("answers in the active language and falls back to English", async () => {
    expect(translate("tabs.files")).toBe("Files");
    await i18n.changeLanguage("ja");
    expect(translate("tabs.files")).toBe("ファイル");
    expect(translate("missing.key")).toBe("missing.key");
  });

  it("picks plural forms", async () => {
    expect(plural(1, "file")).toBe("1 file");
    expect(plural(1200, "file")).toBe("1,200 files");
    await i18n.changeLanguage("ko");
    expect(plural(1, "file")).toBe("파일 1개");
    await i18n.changeLanguage("zh-CN");
    expect(translate("time.days", { count: 3 })).toBe("3 天前");
  });
});
