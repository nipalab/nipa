package preview

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		path     string
		isBinary bool
		want     Kind
	}{
		{"art/hero.PNG", true, KindImage},
		{"tex/rock.dds", true, KindImage},
		{"tex/sky.exr", true, KindImage},
		{"src/hero.psd", true, KindImage},
		{"models/hero.fbx", true, KindModel},
		{"models/hero.gltf", false, KindModel},
		{"sfx/jump.wav", true, KindAudio},
		{"video/intro.mp4", true, KindVideo},
		{"fonts/ui.ttf", true, KindFont},
		{"loc/en.csv", false, KindTable},
		{"loc/utf16.csv", true, KindTable},
		{"src/main.go", false, KindText},
		{"Assets/Hero.prefab", false, KindScene},
		{"Assets/Level.unity", false, KindScene},
		{"Assets/Hero.MAT", false, KindScene},
		{"Assets/Run.overrideController", false, KindScene},
		{"Assets/Ice.physicMaterial", false, KindScene},
		{"Assets/Level.unity", true, KindOpaque},
		{"scenes/level.tscn", false, KindScene},
		{"materials/rock.TRES", false, KindScene},
		{"project.godot", false, KindScene},
		{"art/icon.svg.import", false, KindScene},
		{"scenes/level.scn", true, KindOpaque},
		{"scenes/level.tscn", true, KindOpaque},
		{"src/hero.blend", true, KindOpaque},
		{"Content/Hero.uasset", true, KindOpaque},
		{"README", false, KindText},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, Classify(c.path, c.isBinary), c.path)
	}
}
