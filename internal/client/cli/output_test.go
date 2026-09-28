package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/output"
)

func TestJSONRequested(t *testing.T) {
	t.Setenv(output.EnvFormat, "")
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"flag", []string{"nipa", "status", "--json"}, true},
		{"flag true", []string{"nipa", "status", "--json=true"}, true},
		{"flag false", []string{"nipa", "status", "--json=false"}, false},
		{"after separator", []string{"nipa", "diff", "--", "--json"}, false},
		{"absent", []string{"nipa", "status"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, JSONRequested(tc.args))
		})
	}
}

func TestJSONRequested_Env(t *testing.T) {
	t.Setenv(output.EnvFormat, "json")
	require.True(t, JSONRequested([]string{"nipa", "status"}))
}
