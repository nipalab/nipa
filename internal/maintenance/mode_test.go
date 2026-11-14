package maintenance

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllowedTable(t *testing.T) {
	modes := []Mode{Off, ReadOnly, Full, Frozen}
	table := map[Class][4]bool{
		ClassPublic:  {true, true, true, true},
		ClassAuth:    {true, true, true, false},
		ClassControl: {true, true, true, false},
		ClassRead:    {true, true, false, true},
		ClassWrite:   {true, false, false, false},
	}
	for class, want := range table {
		for i, mode := range modes {
			require.Equal(t, want[i], Allowed(class, mode), "%s in %s", class, mode)
		}
	}
	require.True(t, Allowed(Class("unknown"), Off))
	require.False(t, Allowed(Class("unknown"), ReadOnly))
	require.True(t, Allowed(ClassWrite, Mode("")))
}

func TestAllowedMethodControlFrozen(t *testing.T) {
	require.True(t, AllowedMethod(ClassControl, Frozen, http.MethodGet))
	require.True(t, AllowedMethod(ClassControl, Frozen, http.MethodHead))
	require.False(t, AllowedMethod(ClassControl, Frozen, http.MethodPut))
	require.False(t, AllowedMethod(ClassControl, Frozen, http.MethodDelete))
	require.True(t, AllowedMethod(ClassControl, Full, http.MethodPut))
	require.False(t, AllowedMethod(ClassWrite, ReadOnly, http.MethodGet))
	require.False(t, AllowedMethod(ClassAuth, Frozen, http.MethodGet))
}

func TestStricter(t *testing.T) {
	cases := []struct{ a, b, want Mode }{
		{Off, Off, Off},
		{Off, ReadOnly, ReadOnly},
		{ReadOnly, Off, ReadOnly},
		{ReadOnly, Full, Full},
		{Full, ReadOnly, Full},
		{Full, Frozen, Frozen},
		{Frozen, ReadOnly, Frozen},
		{Mode(""), Off, Off},
		{Mode("bogus"), ReadOnly, ReadOnly},
	}
	for _, c := range cases {
		require.Equal(t, c.want, Stricter(c.a, c.b), "%s vs %s", c.a, c.b)
	}
}

func TestParseModeAndLabel(t *testing.T) {
	for _, m := range []Mode{ReadOnly, Full, Frozen} {
		got, err := ParseMode(string(m))
		require.NoError(t, err)
		require.Equal(t, m, got)
	}
	_, err := ParseMode("off")
	require.Error(t, err)
	_, err = ParseMode("")
	require.Error(t, err)

	require.Equal(t, "read-only", ReadOnly.Label())
	require.Equal(t, "closed", Full.Label())
	require.Equal(t, "frozen", Frozen.Label())
	require.Equal(t, "off", Off.Label())
}
