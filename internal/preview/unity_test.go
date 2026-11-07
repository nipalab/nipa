package preview

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var unityGUIDs = map[string]string{
	"5c1a0f3b2d4e4f6a8b9c0d1e2f3a4b5c": "Assets/Scripts/PlayerController.cs",
	"1234567890abcdef1234567890abcdef": "Assets/Prefabs/Enemy.prefab",
	"9f8e7d6c5b4a39281706f5e4d3c2b1a0": "Assets/Materials/Steel.mat",
	"aa11bb22cc33dd44ee55ff6677889900": "Assets/Data/Sword.asset",
}

func resolveTest(g string) string { return unityGUIDs[g] }

func loadUnity(t *testing.T, name string) *Scene {
	t.Helper()
	data, err := os.ReadFile("testdata/unity/" + name)
	require.NoError(t, err)
	s, err := ParseUnityYAML(data, resolveTest)
	require.NoError(t, err)
	return s
}

func objectByID(t *testing.T, s *Scene, id string) SceneObject {
	t.Helper()
	for _, o := range s.Objects {
		if o.ID == id {
			return o
		}
	}
	t.Fatalf("object %s not found", id)
	return SceneObject{}
}

func TestParseUnityYAML_Scene(t *testing.T) {
	s := loadUnity(t, "Level.unity")
	assert.Equal(t, "unity", s.Engine)
	require.Len(t, s.Objects, 11)

	hero := objectByID(t, s, "100")
	assert.Equal(t, "Hero", hero.Path)
	assert.Equal(t, "GameObject", hero.Type)
	assert.Empty(t, hero.Parent)
	assert.NotContains(t, hero.Props, "m_Component")

	script := objectByID(t, s, "102")
	assert.Equal(t, "Hero > PlayerController", script.Path)
	assert.Equal(t, "PlayerController", script.Type)
	assert.Equal(t, "100", script.Parent)
	assert.Equal(t, "Assets/Scripts/PlayerController.cs", script.Props["m_Script"])
	assert.Equal(t, "Camera", script.Props["target"])
	assert.Equal(t, "guid:bb11bb22cc33dd44ee55ff6677889900", script.Props["weapon"])
	assert.Equal(t, "{x: 1, y: 0, z: 0}", script.Props["waypoints[1]"])
	assert.NotContains(t, script.Props, "m_GameObject")

	sword := objectByID(t, s, "400")
	assert.Equal(t, "Hero/Sword", sword.Path)
	assert.Equal(t, "100", sword.Parent)
	assert.Equal(t, "Assets/Materials/Steel.mat", objectByID(t, s, "402").Props["m_Materials[0]"])
	assert.Equal(t, "Hero/Sword > Transform", objectByID(t, s, "401").Path)

	render := objectByID(t, s, "2")
	assert.Equal(t, "RenderSettings", render.Path)
	assert.Equal(t, "Built-in#10304", render.Props["m_SkyboxMaterial"])
	assert.Equal(t, "{r: 0.5, g: 0.5, b: 0.5, a: 1}", render.Props["m_FogColor"])
	assert.Equal(t, "5", objectByID(t, s, "1").Props["m_OcclusionBakeSettings.smallestOccluder"])

	assert.Equal(t, []string{"Assets/Materials/Steel.mat", "Assets/Scripts/PlayerController.cs", "guid:bb11bb22cc33dd44ee55ff6677889900"}, s.Refs)
}

func TestParseUnityYAML_PrefabInstance(t *testing.T) {
	s := loadUnity(t, "Enemy.prefab")
	inst := objectByID(t, s, "5000")
	assert.Equal(t, "Goblin", inst.Path)
	assert.Equal(t, "PrefabInstance", inst.Type)
	assert.Equal(t, "Assets/Prefabs/Enemy.prefab", inst.Props["m_SourcePrefab"])
	assert.Equal(t, "Goblin", inst.Props["override 7000.m_Name"])
	assert.Equal(t, "3", inst.Props["override 7001.m_LocalPosition.x"])
	assert.Equal(t, "Assets/Materials/Steel.mat", inst.Props["override 7002.m_Materials.Array.data[0]"])
	assert.Equal(t, "None", inst.Props["m_Modification.m_TransformParent"])

	stripped := objectByID(t, s, "5001")
	assert.Equal(t, "Goblin > Transform (prefab)", stripped.Path)
	assert.Equal(t, "5000", stripped.Parent)
	assert.Equal(t, "Assets/Prefabs/Enemy.prefab#7001", stripped.Props["m_CorrespondingSourceObject"])

	added := objectByID(t, s, "6000")
	assert.Equal(t, "Goblin/HealthBar", added.Path)
	assert.Equal(t, "5000", added.Parent)
	assert.Equal(t, "Goblin/HealthBar > RectTransform", objectByID(t, s, "6001").Path)

	comp := objectByID(t, s, "5003")
	assert.Equal(t, "MonoBehaviour", comp.Type)
	assert.Equal(t, "5002", comp.Parent)
	assert.Contains(t, s.Refs, "guid:0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d")
}

func TestParseUnityYAML_NestedPrefabParentAndNames(t *testing.T) {
	data := `%YAML 1.1
--- !u!1 &1
GameObject:
  m_Component:
  - component: {fileID: 2}
  m_Name: Root
--- !u!4 &2
Transform:
  m_GameObject: {fileID: 1}
  m_Father: {fileID: 0}
--- !u!1001 &10
PrefabInstance:
  m_Modification:
    m_TransformParent: {fileID: 2}
    m_Modifications:
    - target: {fileID: 7, guid: 1234567890abcdef1234567890abcdef, type: 3}
      propertyPath: m_IsActive
      value: 0
  m_SourcePrefab: {fileID: 100100000, guid: 1234567890abcdef1234567890abcdef, type: 3}
--- !u!1001 &11
PrefabInstance:
  m_Modification:
    m_TransformParent: {fileID: 2}
  m_SourcePrefab: {fileID: 100100000, guid: ffffffffffffffffffffffffffffffff, type: 3}
--- !u!4 &12 stripped
Transform:
  m_PrefabInstance: {fileID: 0}
--- !u!999 &13
--- !u!114 &14
MonoBehaviour:
  m_GameObject: {fileID: 0}
  m_Name: Settings
  curve: &a
    key: 1
  copy: *a
  empty: {}
  deep: {a: {b: 1}}
`
	s, err := ParseUnityYAML([]byte(data), resolveTest)
	require.NoError(t, err)
	assert.Equal(t, "Root/Enemy", objectByID(t, s, "10").Path)
	assert.Equal(t, "1", objectByID(t, s, "10").Parent)
	assert.Equal(t, "0", objectByID(t, s, "10").Props["override 7.m_IsActive"])
	assert.Equal(t, "Root/PrefabInstance", objectByID(t, s, "11").Path)
	assert.Equal(t, "Transform (prefab)", objectByID(t, s, "12").Path)
	assert.Equal(t, "Class999", objectByID(t, s, "13").Type)
	settings := objectByID(t, s, "14")
	assert.Equal(t, "Settings", settings.Path)
	assert.Equal(t, "*a", settings.Props["copy"])
	assert.Equal(t, "1", settings.Props["curve.key"])
	assert.Equal(t, "{}", settings.Props["empty"])
	assert.Equal(t, "{b: 1}", settings.Props["deep.a"])
}

func TestParseUnityYAML_Errors(t *testing.T) {
	bin, err := os.ReadFile("testdata/unity/Binary.asset")
	require.NoError(t, err)
	_, err = ParseUnityYAML(bin, nil)
	require.ErrorIs(t, err, ErrUnsupported)
	assert.Contains(t, err.Error(), "Force Text")

	_, err = ParseUnityYAML([]byte("%YAML 1.1\n--- !u!x &1\nA: 1\n"), nil)
	assert.ErrorIs(t, err, errScene)
	_, err = ParseUnityYAML([]byte("%YAML 1.1\n--- !u!1 &1\nA: [\n"), nil)
	assert.ErrorIs(t, err, errScene)

	old := MaxSceneObjects
	MaxSceneObjects = 1
	_, err = ParseUnityYAML([]byte("%YAML 1.1\n--- !u!1 &1\nA: 1\n--- !u!1 &2\nA: 1\n"), nil)
	MaxSceneObjects = old
	assert.ErrorIs(t, err, errSceneTooLarge)

	oldProps := MaxSceneProps
	MaxSceneProps = 2
	_, err = ParseUnityYAML([]byte("%YAML 1.1\n--- !u!1 &1\nA:\n  a: 1\n  b: 2\n  c: 3\n"), nil)
	MaxSceneProps = oldProps
	assert.ErrorIs(t, err, errSceneTooLarge)
}

func TestParseUnityYAML_BOMNilResolverDuplicatesAndCycles(t *testing.T) {
	data := "\xef\xbb\xbf%YAML 1.1\n" +
		"--- !u!1 &1\nGameObject:\n  m_Component:\n  - component: {fileID: 2}\n  m_Name: A\n" +
		"--- !u!4 &2\nTransform:\n  m_GameObject: {fileID: 1}\n  m_Father: {fileID: 4}\n" +
		"--- !u!1 &3\nGameObject:\n  m_Component:\n  - component: {fileID: 4}\n  m_Name: B\n" +
		"--- !u!4 &4\nTransform:\n  m_GameObject: {fileID: 3}\n  m_Father: {fileID: 2}\n" +
		"--- !u!1 &1\nGameObject:\n  m_Name: Dup\n" +
		"--- !u!114 &5\nMonoBehaviour:\n  m_GameObject: {fileID: 1}\n  m_Script: {fileID: 11500000, guid: abc, type: 3}\n  list:\n  - a: 1\n  - a: 2\n  missing: {fileID: 99}\n"
	s, err := ParseUnityYAML([]byte(data), nil)
	require.NoError(t, err)
	require.Len(t, s.Objects, 5)
	assert.NotEmpty(t, objectByID(t, s, "1").Path)
	mb := objectByID(t, s, "5")
	assert.Equal(t, "MonoBehaviour", mb.Type)
	assert.Equal(t, "guid:abc", mb.Props["m_Script"])
	assert.Equal(t, "1", mb.Props["list[0].a"])
	assert.Equal(t, "#99", mb.Props["missing"])
}

func TestClip(t *testing.T) {
	assert.Equal(t, "short", clip("short"))
	long := strings.Repeat("é", 300)
	c := clip(long)
	assert.Less(t, len(c), len(long))
	assert.Contains(t, c, "600 bytes")
	assert.NotEqual(t, c, clip(long[:598]+"a"))
}

func TestJoinPath(t *testing.T) {
	assert.Equal(t, "a/b", joinPath("a", "/", "b"))
	long := strings.Repeat("é", maxPathLen)
	got := joinPath(long, "/", "b")
	assert.True(t, strings.HasPrefix(got, "…é"))
	assert.Less(t, len(got), maxPathLen+8)
}
