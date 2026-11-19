package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseOrigin(t *testing.T) {
	sha1 := strings.Repeat("ab", 20)
	sha256 := strings.Repeat("0f", 32)
	cases := []struct {
		name  string
		in    string
		want  Origin
		error string
	}{
		{name: "git sha1", in: "git:default:" + sha1, want: Origin{System: "git", Source: "default", NativeID: sha1}},
		{name: "git sha256", in: "git:game:" + sha256, want: Origin{System: "git", Source: "game", NativeID: sha256}},
		{name: "hg", in: "hg:default:" + sha1, want: Origin{System: "hg", Source: "default", NativeID: sha1}},
		{name: "svn", in: "svn:9f1c-uuid:1234:/branches/my x", want: Origin{System: "svn", Source: "9f1c-uuid", NativeID: "1234", Qualifier: "/branches/my x"}},
		{name: "svn revision zero", in: "svn:u:0:/trunk", want: Origin{System: "svn", Source: "u", NativeID: "0", Qualifier: "/trunk"}},
		{name: "p4 qualifier with colons", in: "p4:srv:58211://depot/main", want: Origin{System: "p4", Source: "srv", NativeID: "58211", Qualifier: "//depot/main"}},

		{name: "empty", in: "", error: "must not be empty"},
		{name: "too long", in: "git:" + strings.Repeat("s", MaxOriginBytes) + ":" + sha1, error: "longer than"},
		{name: "invalid utf8", in: "git:\xff:" + sha1, error: "UTF-8"},
		{name: "two parts", in: "git:" + sha1, error: "want <system>"},
		{name: "unknown system", in: "cvs:default:" + sha1, error: "unknown system"},
		{name: "uppercase system", in: "GIT:default:" + sha1, error: "unknown system"},
		{name: "empty source", in: "git::" + sha1, error: "source must not be empty"},
		{name: "space in source", in: "git:my repo:" + sha1, error: "whitespace"},
		{name: "control in id", in: "git:default:\t" + sha1, error: "whitespace"},
		{name: "control in qualifier", in: "svn:u:1:/a\x01b", error: "control characters in the qualifier"},
		{name: "git short id", in: "git:default:abc123", error: "invalid git id"},
		{name: "git uppercase hex", in: "git:default:" + strings.ToUpper(sha1), error: "invalid git id"},
		{name: "git qualifier", in: "git:default:" + sha1 + ":x", error: "take no qualifier"},
		{name: "git empty qualifier", in: "git:default:" + sha1 + ":", error: "take no qualifier"},
		{name: "hg non hex", in: "hg:default:" + strings.Repeat("zz", 20), error: "invalid hg id"},
		{name: "svn non decimal", in: "svn:u:12a:/trunk", error: "invalid svn id"},
		{name: "svn leading zero", in: "svn:u:012:/trunk", error: "invalid svn id"},
		{name: "svn no qualifier", in: "svn:u:12", error: "need a qualifier"},
		{name: "p4 empty qualifier", in: "p4:srv:12:", error: "need a qualifier"},
		{name: "p4 huge id", in: "p4:srv:" + strings.Repeat("9", 20) + "://depot", error: "invalid p4 id"},
		{name: "p4 empty id", in: "p4:srv::x", error: "invalid p4 id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOrigin(tc.in)
			if tc.error != "" {
				require.Error(t, err)
				var de *Error
				require.ErrorAs(t, err, &de)
				require.Equal(t, 400, de.Code)
				require.Contains(t, err.Error(), tc.error)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestOriginDisplay(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		origin, system, short string
	}{
		{"git:default:" + sha, "Git", "0123456"},
		{"hg:default:" + sha, "Mercurial", "0123456"},
		{"svn:repo:1234:trunk", "SVN", "r1234"},
		{"p4:depot:58211://depot/main", "Perforce", "CL 58211"},
		{"cvs:src:abc", "cvs", "abc"},
		{"git:default:abc", "Git", "abc"},
		{"", "", ""},
		{"git:default", "", ""},
		{"git::", "", ""},
	}
	for _, tc := range cases {
		require.Equal(t, tc.system, OriginSystemName(tc.origin), tc.origin)
		require.Equal(t, tc.short, OriginShortID(tc.origin), tc.origin)
	}
}

func TestCommitLogEntryDisplayAuthor(t *testing.T) {
	native := &CommitLogEntry{AuthorName: "Ann", AuthorEmail: "ann@nipa.dev"}
	name, email := native.DisplayAuthor()
	require.Equal(t, "Ann", name)
	require.Equal(t, "ann@nipa.dev", email)

	imported := &CommitLogEntry{
		Commit:      Commit{AuthorName: "Linus", AuthorEmail: "linus@git.dev"},
		AuthorName:  "Importer",
		AuthorEmail: "importer@nipa.dev",
	}
	name, email = imported.DisplayAuthor()
	require.Equal(t, "Linus", name)
	require.Equal(t, "linus@git.dev", email)
}
