package lang

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestResolve(t *testing.T) {
	require.Equal(t, Japanese, Resolve("ja", env(nil)))
	require.Equal(t, Korean, Resolve("system", env(map[string]string{"LANG": "ko_KR.UTF-8"})))
	require.Equal(t, Chinese, Resolve("", env(map[string]string{"LC_ALL": "zh_CN.UTF-8", "LANG": "en_US"})))
	require.Equal(t, Indonesian, Resolve("", env(map[string]string{"LANGUAGE": "id"})))
	require.Equal(t, English, Resolve("", env(map[string]string{"LANG": "C"})))
	require.Equal(t, English, Resolve("fr", env(nil)))
}

func TestFromLocale(t *testing.T) {
	require.Equal(t, English, FromLocale("zh-Hant-TW"))
	require.Equal(t, English, FromLocale("zh_HK"))
	require.Equal(t, Chinese, FromLocale("zh-Hans"))
	require.Equal(t, Indonesian, FromLocale("in-ID"))
	require.Equal(t, English, FromLocale("de_DE"))
}

func TestTranslate(t *testing.T) {
	require.Equal(t, "Open Nipa", English.T("tray.open"))
	require.Equal(t, "Nipa を開く", Japanese.T("tray.open"))
	require.Equal(t, "game: new commit on main", English.T("notify.commit", "game", "main"))
	require.Equal(t, "missing.key", English.T("missing.key"))
	require.Equal(t, "Open Nipa", Language("xx").T("tray.open"))
}

func TestPlural(t *testing.T) {
	require.Equal(t, "1 new commit", English.N("tray.commits", 1))
	require.Equal(t, "2 new commits", English.N("tray.commits", 2))
	require.Equal(t, "101+ new commits", English.N("tray.commits", 101))
	require.Equal(t, "新しいコミット 1 件", Japanese.N("tray.commits", 1))
}

func TestCatalogsMatchEnglish(t *testing.T) {
	base := func(k string) string {
		for _, suffix := range []string{".one", ".other"} {
			if len(k) > len(suffix) && k[len(k)-len(suffix):] == suffix {
				return k[:len(k)-len(suffix)]
			}
		}
		return k
	}
	english := map[string]bool{}
	for k := range catalogs[English] {
		english[base(k)] = true
	}
	for _, l := range all {
		got := map[string]bool{}
		for k := range catalogs[l] {
			got[base(k)] = true
		}
		require.Equal(t, english, got, string(l))
	}
}
