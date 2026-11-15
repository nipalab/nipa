// Package lang holds the few strings the Go side shows to people (tray menu,
// notifications) in the languages of the app.
package lang

import (
	"fmt"
	"strings"
)

type Language string

const (
	English    Language = "en"
	Indonesian Language = "id"
	Japanese   Language = "ja"
	Chinese    Language = "zh-CN"
	Korean     Language = "ko"
)

// SettingKey is the interface-state entry the web side stores the choice in;
// "system" or empty follows the operating system.
const SettingKey = "nipa.language"

var all = []Language{English, Indonesian, Japanese, Chinese, Korean}

// Resolve picks the language from the stored setting, else from the locale
// environment variables.
func Resolve(setting string, env func(string) string) Language {
	for _, l := range all {
		if setting == string(l) {
			return l
		}
	}
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		if v := env(name); v != "" && v != "C" && v != "POSIX" {
			return FromLocale(v)
		}
	}
	return English
}

// FromLocale maps a locale such as ja_JP.UTF-8 or zh-Hans-CN to a language.
func FromLocale(locale string) Language {
	l := strings.ToLower(strings.ReplaceAll(locale, "_", "-"))
	switch {
	case strings.HasPrefix(l, "id"), strings.HasPrefix(l, "in"):
		return Indonesian
	case strings.HasPrefix(l, "ja"):
		return Japanese
	case strings.HasPrefix(l, "ko"):
		return Korean
	case strings.HasPrefix(l, "zh"):
		for _, traditional := range []string{"hant", "-tw", "-hk", "-mo"} {
			if strings.Contains(l, traditional) {
				return English
			}
		}
		return Chinese
	}
	return English
}

// T formats the message for key; a missing key falls back to English, then to
// the key itself.
func (l Language) T(key string, args ...any) string {
	msg, ok := catalogs[l][key]
	if !ok {
		msg, ok = catalogs[English][key]
	}
	if !ok {
		return key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// N picks the "<key>.one" or "<key>.other" message for n and formats it with
// Count(n) first; a language without a singular form only defines ".other".
func (l Language) N(key string, n int, args ...any) string {
	form := ".other"
	if n == 1 {
		if _, ok := catalogs[l][key+".one"]; ok {
			form = ".one"
		}
	}
	return l.T(key+form, append([]any{Count(n)}, args...)...)
}

// Count renders n the way the tray does: 100 and more read "100+".
func Count(n int) string {
	if n >= 100 {
		return fmt.Sprintf("%d+", n)
	}
	return fmt.Sprintf("%d", n)
}
