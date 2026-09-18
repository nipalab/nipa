package difftool

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	p := Pair{Path: "P", Status: "S", Local: "L", Remote: "R"}
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"simple", "meld $LOCAL $REMOTE", []string{"meld", "L", "R"}},
		{"single quotes", "code --diff '$LOCAL' \"$REMOTE\"", []string{"code", "--diff", "L", "R"}},
		{"double quotes", `tool "a b" c`, []string{"tool", "a b", "c"}},
		{"double quote escape", `tool "a\"b" c`, []string{"tool", `a"b`, "c"}},
		{"escaped space", `tool a\ b c`, []string{"tool", "a b", "c"}},
		{"escaped dollar", `tool \$UNKNOWN`, []string{"tool", "$UNKNOWN"}},
		{"escaped backslash", `tool a\\b`, []string{"tool", `a\b`}},
		{"windows path", `C:\tools\diff.exe $LOCAL`, []string{`C:\tools\diff.exe`, "L"}},
		{"tabs and newlines", "tool\ta\nb", []string{"tool", "a", "b"}},
		{"quoted empty", `tool "" x`, []string{"tool", "", "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			require.NoError(t, err)
			argv, _ := got.Expand(p, "OD", "ND")
			require.Equal(t, tt.want, argv)
		})
	}
}

func TestParse_Errors(t *testing.T) {
	for _, in := range []string{"", "   ", "'oops", `"oops`} {
		_, err := Parse(in)
		require.Error(t, err, in)
	}
}

func TestExpand(t *testing.T) {
	tmpl, err := Parse("tool $LOCAL $REMOTE $LOCAL_DIR $REMOTE_DIR $PATH $STATUS ${PATH} $UNKNOWN $ $HOME $$")
	require.NoError(t, err)
	p := Pair{Path: "a/b.png", Status: "M", Local: "/tmp/o/a/b.png", Remote: "/tmp/n/a/b.png"}
	argv, env := tmpl.Expand(p, "/tmp/o", "/tmp/n")
	require.Equal(t, []string{
		"tool",
		"/tmp/o/a/b.png",
		"/tmp/n/a/b.png",
		"/tmp/o",
		"/tmp/n",
		"a/b.png",
		"M",
		"a/b.png",
		"$UNKNOWN",
		"$",
		"$HOME",
		"$",
	}, argv)
	require.Equal(t, []string{
		"NIPA_PATH=a/b.png",
		"NIPA_STATUS=M",
		"NIPA_LOCAL=/tmp/o/a/b.png",
		"NIPA_REMOTE=/tmp/n/a/b.png",
		"NIPA_LOCAL_DIR=/tmp/o",
		"NIPA_REMOTE_DIR=/tmp/n",
	}, env)
}

func TestExpand_BraceForms(t *testing.T) {
	tmpl, err := Parse("tool ${LOCAL} ${REMOTE} ${LOCAL_DIR} ${STATUS} ${} ${UNCLOSED")
	require.NoError(t, err)
	p := Pair{Path: "f.txt", Status: "A", Local: "/o", Remote: "/n"}
	argv, _ := tmpl.Expand(p, "/od", "/nd")
	require.Equal(t, []string{"tool", "/o", "/n", "/od", "A", "${}", "${UNCLOSED"}, argv)
}
