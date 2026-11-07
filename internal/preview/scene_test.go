package preview

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func changeByID(t *testing.T, d *SceneCompare, id string) SceneChange {
	t.Helper()
	for _, c := range d.Changes {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("change %s not found", id)
	return SceneChange{}
}

func TestDiffScenes_UnityLevel(t *testing.T) {
	d := DiffScenes(loadUnity(t, "Level_old.unity"), loadUnity(t, "Level.unity"))
	assert.Equal(t, "unity", d.Engine)
	assert.Equal(t, 3, d.Added)
	assert.Equal(t, 2, d.Removed)
	assert.Equal(t, 6, d.Changed)

	camera := changeByID(t, d, "200")
	assert.Equal(t, "moved", camera.Status)
	assert.Equal(t, []PropChange{{Key: "parent", Old: "Player", New: "(root)"}}, camera.Props)

	assert.Equal(t, []PropChange{{Key: "m_Name", Old: "Player", New: "Hero"}}, changeByID(t, d, "100").Props)
	assert.Equal(t, []PropChange{
		{Key: "speed", Old: "5", New: "7.5"},
		{Key: "target", New: "Camera"},
		{Key: "weapon", Old: "Assets/Data/Sword.asset", New: "guid:bb11bb22cc33dd44ee55ff6677889900"},
	}, changeByID(t, d, "102").Props)

	removed := changeByID(t, d, "300")
	assert.Equal(t, "removed", removed.Status)
	assert.Equal(t, []PropChange{{Key: "m_Name", Old: "OldLight"}}, removed.Props)
	assert.Equal(t, "added", changeByID(t, d, "400").Status)

	ids := map[string]SceneObject{}
	for _, o := range d.Objects {
		ids[o.ID] = o
		assert.Nil(t, o.Props)
	}
	assert.Contains(t, ids, "300")
	assert.Contains(t, ids, "100")
	assert.NotContains(t, ids, "1")
	assert.Equal(t, "400", ids["402"].Parent)

	assert.Equal(t, []RefChange{
		{Path: "Assets/Materials/Steel.mat", Status: "added"},
		{Path: "guid:bb11bb22cc33dd44ee55ff6677889900", Status: "added"},
		{Path: "Assets/Data/Sword.asset", Status: "removed"},
	}, d.Refs)
	for i := 1; i < len(d.Changes); i++ {
		assert.LessOrEqual(t, d.Changes[i-1].Path, d.Changes[i].Path)
	}
}

func TestDiffScenes_Material(t *testing.T) {
	d := DiffScenes(loadUnity(t, "Hero_old.mat"), loadUnity(t, "Hero.mat"))
	require.Len(t, d.Changes, 1)
	c := d.Changes[0]
	assert.Equal(t, "changed", c.Status)
	assert.Equal(t, []PropChange{
		{Key: "m_SavedProperties.m_Colors._Color", Old: "{r: 1, g: 1, b: 1, a: 1}", New: "{r: 1, g: 0.25, b: 0.25, a: 1}"},
		{Key: "m_SavedProperties.m_Floats._Cutoff", New: "0.5"},
		{Key: "m_SavedProperties.m_TexEnvs._BumpMap.m_Texture", Old: "None", New: "guid:4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f4f"},
	}, c.Props)
}

func TestDiffScenes_NilSidesAndReorder(t *testing.T) {
	s := loadUnity(t, "Enemy.prefab")
	added := DiffScenes(nil, s)
	assert.Equal(t, len(s.Objects), added.Added)
	assert.Equal(t, "unity", added.Engine)
	assert.Len(t, added.Refs, len(s.Refs))

	removed := DiffScenes(s, nil)
	assert.Equal(t, len(s.Objects), removed.Removed)
	assert.Equal(t, "unity", removed.Engine)

	empty := DiffScenes(nil, nil)
	assert.Empty(t, empty.Changes)
	assert.NotNil(t, empty.Objects)
	assert.NotNil(t, empty.Refs)

	reordered := &Scene{Engine: "unity", Objects: append([]SceneObject{}, s.Objects...), Refs: s.Refs}
	for i, j := 0, len(reordered.Objects)-1; i < j; i, j = i+1, j-1 {
		reordered.Objects[i], reordered.Objects[j] = reordered.Objects[j], reordered.Objects[i]
	}
	same := DiffScenes(s, reordered)
	assert.Empty(t, same.Changes)
	assert.Empty(t, same.Refs)
}

func TestDiffScenes_TypeChangeAndOrphans(t *testing.T) {
	old := &Scene{Objects: []SceneObject{
		{ID: "1", Path: "A", Type: "Light"},
		{ID: "2", Path: "A/B", Type: "GameObject", Parent: "1"},
		{ID: "3", Path: "A/B/C", Type: "GameObject", Parent: "2"},
	}}
	new := &Scene{Objects: []SceneObject{
		{ID: "1", Path: "A", Type: "Camera"},
		{ID: "4", Path: "D", Type: "GameObject", Parent: "9"},
		{ID: "4", Path: "dup", Type: "GameObject"},
	}}
	d := DiffScenes(old, new)
	assert.Equal(t, []PropChange{{Key: "type", Old: "Light", New: "Camera"}}, changeByID(t, d, "1").Props)
	assert.Equal(t, "added", changeByID(t, d, "4").Status)
	assert.Equal(t, 2, d.Removed)
	ids := map[string]string{}
	for _, o := range d.Objects {
		ids[o.ID] = o.Path
	}
	assert.Equal(t, map[string]string{"1": "A", "2": "A/B", "3": "A/B/C", "4": "D"}, ids)
	assert.Equal(t, "#7", pathOf(map[string]*SceneObject{}, "7"))
}
