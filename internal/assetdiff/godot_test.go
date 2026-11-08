package assetdiff

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const godotBase = `[gd_scene format=3]

[node name="Main" type="Node2D"]

[node name="Player" type="Node2D" parent="."]
speed = 5
`

const godotOurs = `[gd_scene format=3]

[node name="Main" type="Node2D"]

[node name="Player" type="Node2D" parent="."]
speed = 7
`

const godotTheirs = `[gd_scene format=3]

[node name="Main" type="Node2D"]
visible = false

[node name="Player" type="Node2D" parent="."]
speed = 5
`

const godotOther = `[gd_scene format=3]

[node name="Main" type="Node2D"]

[node name="Player" type="Node2D" parent="."]
speed = 9
`

func rev(id string) *Ref { return &Ref{Source: "revision", Revision: id, Path: "level.tscn"} }

func TestMergeGodot(t *testing.T) {
	load := func(sides map[string]string) Loader {
		f := files{}
		for id, v := range sides {
			f["revision:"+id] = []byte(v)
		}
		return func(c context.Context, ref Ref, limit int64) ([]byte, error) {
			return f.load(c, Ref{Source: "revision", Path: ref.Revision}, limit)
		}
	}
	m, err := MergeGodot(ctx, load(map[string]string{"b": godotBase, "o": godotOurs, "t": godotTheirs}), rev("b"), rev("o"), rev("t"))
	require.NoError(t, err)
	assert.True(t, m.Clean)
	assert.Contains(t, string(m.Merged), "speed = 7")
	assert.Contains(t, string(m.Merged), "visible = false")

	m, err = MergeGodot(ctx, load(map[string]string{"b": godotBase, "o": godotOurs, "t": godotOther}), rev("b"), rev("o"), rev("t"))
	require.NoError(t, err)
	assert.False(t, m.Clean)
	assert.NotEmpty(t, m.Conflicts)

	m, err = MergeGodot(ctx, load(map[string]string{"o": godotOurs, "t": godotOurs}), nil, rev("o"), rev("t"))
	require.NoError(t, err)
	assert.True(t, m.Clean)

	m, err = MergeGodot(ctx, load(map[string]string{"b": godotBase, "o": godotOurs}), rev("b"), rev("o"), rev("t"))
	require.NoError(t, err)
	assert.Equal(t, []GodotConflict{{Message: "theirs deleted the file: pick ours or theirs"}}, m.Conflicts)
	m, err = MergeGodot(ctx, load(map[string]string{"b": godotBase}), rev("b"), rev("o"), rev("t"))
	require.NoError(t, err)
	assert.Equal(t, "ours deleted the file: pick ours or theirs", m.Conflicts[0].Message)

	_, err = MergeGodot(ctx, load(map[string]string{"b": "[node name=\"A\"\n", "o": godotOurs, "t": godotTheirs}), rev("b"), rev("o"), rev("t"))
	var me *MergeError
	require.ErrorAs(t, err, &me)
	assert.Contains(t, me.Error(), "base:")
	assert.NotNil(t, me.Unwrap())

	_, err = MergeGodot(ctx, load(map[string]string{"b": "ERR"}), rev("b"), rev("o"), rev("t"))
	assert.ErrorContains(t, err, "offline")
}
