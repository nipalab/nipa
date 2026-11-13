// Package assetdiff turns two versions of a file into a compact, JSON-friendly
// structured comparison (scene objects, image pixels, model and audio stats,
// table cells, text lines) on top of internal/preview. It is pure: callers
// supply the bytes through a Loader.
package assetdiff

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/nipalab/nipa/internal/preview"
)

const (
	MaxSceneBytes  int64 = 16 << 20
	MaxModelBytes  int64 = 256 << 20
	MaxAudioBytes  int64 = 256 << 20
	MaxImageBytes  int64 = 256 << 20
	MaxTableBytes  int64 = 16 << 20
	MaxTextBytes   int64 = 4 << 20
	MaxOpaqueBytes int64 = 64 << 20
)

const (
	StatusAdded     = "added"
	StatusRemoved   = "removed"
	StatusModified  = "modified"
	StatusUnchanged = "unchanged"
)

var (
	// ErrMissing is returned by a Loader when the side has no such file.
	ErrMissing = errors.New("assetdiff: file is missing")
	// ErrTooLarge is returned by a Loader when the file exceeds the limit.
	ErrTooLarge = errors.New("assetdiff: file is too large")
)

// Ref names one file version, mirroring the client's blob request: Source is
// working, snapshot or revision.
type Ref struct {
	Source   string `json:"source"`
	Revision string `json:"revision,omitempty"`
	Path     string `json:"path"`
}

// Loader reads at most limit bytes of ref, failing with ErrTooLarge beyond.
type Loader func(ctx context.Context, ref Ref, limit int64) ([]byte, error)

type Options struct {
	Images    bool
	ThumbSize int
	MaxRows   int
	// Sheet selects the workbook sheet whose rows are returned; empty picks
	// the first changed one.
	Sheet      string
	MaxChanges int
	MaxProps   int
	MaxFiles   int
	Threshold  uint8
	// GUIDs returns the Unity GUID resolver for the asset at path; nil
	// leaves GUIDs unresolved.
	GUIDs func(path string) func(guid string) string
}

func (o Options) withDefaults() Options {
	if o.ThumbSize <= 0 {
		o.ThumbSize = 256
	}
	if o.MaxRows <= 0 {
		o.MaxRows = 200
	}
	if o.MaxChanges <= 0 {
		o.MaxChanges = 500
	}
	if o.MaxProps <= 0 {
		o.MaxProps = 50
	}
	if o.MaxFiles <= 0 {
		o.MaxFiles = 50
	}
	return o
}

type Result struct {
	Path    string       `json:"path"`
	OldPath string       `json:"old_path,omitempty"`
	Kind    preview.Kind `json:"kind"`
	Status  string       `json:"status"`
	OldSize int64        `json:"old_size"`
	NewSize int64        `json:"new_size"`
	Error   string       `json:"error,omitempty"`
	Scene   *SceneResult `json:"scene,omitempty"`
	Image   *ImageResult `json:"image,omitempty"`
	Model   *ModelResult `json:"model,omitempty"`
	Audio   *AudioResult `json:"audio,omitempty"`
	Table   *TableResult `json:"table,omitempty"`
	Text    *TextResult  `json:"text,omitempty"`
}

type side struct {
	ref     *Ref
	data    []byte
	present bool
}

// Compare loads both versions and compares them according to the kind of
// the file. A nil side, or one the Loader reports missing, is absent.
// Parse failures and oversized files are reported in Result.Error; only
// Loader failures and cancellation return an error.
func Compare(ctx context.Context, load Loader, old, new *Ref, opts Options) (*Result, error) {
	if old == nil && new == nil {
		return nil, errors.New("assetdiff: nothing to compare")
	}
	opts = opts.withDefaults()
	res := &Result{}
	main := new
	if main == nil {
		main = old
	}
	res.Path = main.Path
	if old != nil && new != nil && old.Path != new.Path {
		res.OldPath = old.Path
	}
	kind := preview.Classify(main.Path, false)
	limit := limitFor(kind)
	sides := [2]side{{ref: old}, {ref: new}}
	tooLarge := false
	for i := range sides {
		s := &sides[i]
		if s.ref == nil {
			continue
		}
		data, err := load(ctx, *s.ref, limit)
		switch {
		case errors.Is(err, ErrMissing):
			continue
		case errors.Is(err, ErrTooLarge):
			tooLarge, s.present = true, true
			continue
		case err != nil:
			return nil, err
		}
		s.data, s.present = data, true
	}
	if kind == preview.KindText {
		for _, s := range sides {
			if s.present && looksBinary(s.data) {
				kind = preview.KindOpaque
			}
		}
	}
	res.Kind = kind
	res.OldSize, res.NewSize = int64(len(sides[0].data)), int64(len(sides[1].data))
	switch {
	case !sides[0].present && !sides[1].present:
		return nil, ErrMissing
	case !sides[0].present:
		res.Status = StatusAdded
	case !sides[1].present:
		res.Status = StatusRemoved
	case !tooLarge && bytes.Equal(sides[0].data, sides[1].data):
		res.Status = StatusUnchanged
		return res, nil
	default:
		res.Status = StatusModified
	}
	if tooLarge {
		res.Error = "the file is too large to compare"
		return res, nil
	}
	var err error
	switch kind {
	case preview.KindScene:
		res.Scene, err = compareScene(sides, opts)
	case preview.KindImage:
		res.Image, err = compareImage(sides, opts)
	case preview.KindModel:
		res.Model, err = compareModel(sides)
	case preview.KindAudio:
		res.Audio, err = compareAudio(sides)
	case preview.KindTable:
		res.Table, err = compareTable(sides, opts)
	case preview.KindText:
		res.Text = compareText(sides)
	}
	if err != nil {
		res.Error = strings.TrimPrefix(err.Error(), "preview: ")
	}
	return res, nil
}

func limitFor(kind preview.Kind) int64 {
	switch kind {
	case preview.KindScene:
		return MaxSceneBytes
	case preview.KindModel:
		return MaxModelBytes
	case preview.KindAudio:
		return MaxAudioBytes
	case preview.KindImage:
		return MaxImageBytes
	case preview.KindTable:
		return MaxTableBytes
	}
	return MaxOpaqueBytes
}

func looksBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0
}
