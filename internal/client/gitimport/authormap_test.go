package gitimport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAuthorMap(t *testing.T) {
	m, err := ParseAuthorMap(strings.NewReader(`
# team
Ann Author <ann@Example.com> = 1a
<ANN@example.com> = 2b
   <bob@example.com>   =   3c
`))
	require.NoError(t, err)
	require.Equal(t, 3, m.Len())

	user, ok := m.Lookup("Ann Author", "ann@example.com")
	require.True(t, ok)
	require.Equal(t, "1a", user)
	user, ok = m.Lookup("Someone Else", "Ann@Example.com")
	require.True(t, ok)
	require.Equal(t, "2b", user)
	user, ok = m.Lookup("Bob", " bob@example.com ")
	require.True(t, ok)
	require.Equal(t, "3c", user)
	_, ok = m.Lookup("Cid", "cid@example.com")
	require.False(t, ok)

	var nilMap *AuthorMap
	require.Zero(t, nilMap.Len())
	_, ok = nilMap.Lookup("Ann", "ann@example.com")
	require.False(t, ok)
}

func TestParseAuthorMapErrors(t *testing.T) {
	cases := map[string]struct {
		input string
		line  int
		msg   string
	}{
		"no equals":       {"Ann <ann@example.com> 1a", 1, "want `Name <email> = user-id`"},
		"missing user":    {"\nAnn <ann@example.com> =", 2, "missing user id"},
		"invalid user":    {"Ann <ann@example.com> = not-an-id", 1, "invalid user id"},
		"missing email":   {"Ann = 1a", 1, "missing <email>"},
		"empty email":     {"Ann <> = 1a", 1, "invalid email"},
		"nested brackets": {"Ann <a<b@example.com> = 1a", 1, "invalid email"},
		"duplicate email": {"<a@example.com> = 1\n# c\n<A@example.com> = 2", 3, "mapped twice"},
		"duplicate name":  {"Ann <a@example.com> = 1\nAnn <a@example.com> = 2", 2, "mapped twice"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseAuthorMap(strings.NewReader(tc.input))
			var me *AuthorMapError
			require.True(t, errors.As(err, &me), "%v", err)
			require.Equal(t, tc.line, me.Line)
			require.Contains(t, me.Error(), tc.msg)
			require.Contains(t, me.Error(), "line")
		})
	}
}

func TestLoadAuthorMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authors")
	require.NoError(t, os.WriteFile(path, []byte("<a@example.com> = 1\n"), 0o600))
	m, err := LoadAuthorMap(path)
	require.NoError(t, err)
	require.Equal(t, 1, m.Len())

	_, err = LoadAuthorMap(filepath.Join(t.TempDir(), "missing"))
	require.ErrorContains(t, err, "open author map")

	_, err = ParseAuthorMap(errReader{})
	require.ErrorContains(t, err, "read author map")
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }
