package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"path"
	"strings"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/preview"
)

const (
	maxModelBytes  = 256 << 20
	maxRenderBytes = 64 << 20
	maxModelFiles  = 64
)

var (
	errBlobMissing  = errors.New("file not found")
	errBlobTooLarge = errors.New("file too large")
)

type ModelStats struct {
	Generator    string    `json:"generator"`
	Meshes       int       `json:"meshes"`
	Primitives   int       `json:"primitives"`
	Vertices     int       `json:"vertices"`
	Triangles    int       `json:"triangles"`
	Materials    []string  `json:"materials"`
	Textures     int       `json:"textures"`
	Nodes        int       `json:"nodes"`
	Skins        int       `json:"skins"`
	Joints       int       `json:"joints"`
	Animations   []string  `json:"animations"`
	MorphTargets int       `json:"morphTargets"`
	Min          []float64 `json:"min"` // nil when the model has no bounds
	Max          []float64 `json:"max"`
}

// ModelSide is one version of a model with what the viewer needs to render
// it: its bytes and the files it references, all base64.
type ModelSide struct {
	Path   string `json:"path"`
	Format string `json:"format"` // extension without the dot
	Size   int64  `json:"size"`
	// Stats is nil for formats without a Go parser (FBX, USD); the viewer
	// then counts from the loaded scene. StatsError says why.
	Stats      *ModelStats       `json:"stats"`
	StatsError string            `json:"statsError,omitempty"`
	Data       string            `json:"data"`
	Files      map[string]string `json:"files"`
	Missing    []string          `json:"missing"`
	// TooLarge means the model and its files exceed the render budget; only
	// the stats are returned.
	TooLarge bool `json:"tooLarge,omitempty"`
}

type ModelCompare struct {
	Old *ModelSide `json:"old"`
	New *ModelSide `json:"new"`
}

// ModelDiff loads the old and/or new version of a 3D model for the viewer.
func (s *Service) ModelDiff(ctx context.Context, root string, old, new *ImageRef) (*ModelCompare, error) {
	if old == nil && new == nil {
		return nil, errors.New("nothing to compare")
	}
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	out := &ModelCompare{}
	for _, side := range []struct {
		ref  *ImageRef
		dest **ModelSide
	}{{old, &out.Old}, {new, &out.New}} {
		if side.ref == nil {
			continue
		}
		m, err := s.loadModel(ctx, client, root, side.ref)
		if err != nil {
			return nil, err
		}
		*side.dest = m
	}
	return out, nil
}

func (s *Service) loadModel(ctx context.Context, client daemonpb.NipaDaemonClient, root string, ref *ImageRef) (*ModelSide, error) {
	info, data, err := s.readBlob(ctx, client, root, blobRef(ref), maxModelBytes)
	if errors.Is(err, errBlobTooLarge) {
		return &ModelSide{Path: ref.Path, Format: modelFormat(ref.Path), Size: info.GetSizeBytes(), StatsError: "the model is too large to read", Files: map[string]string{}, Missing: []string{}, TooLarge: true}, nil
	}
	if err != nil {
		return nil, err
	}
	side := &ModelSide{Path: ref.Path, Format: modelFormat(ref.Path), Size: int64(len(data)), Files: map[string]string{}, Missing: []string{}}
	var deps []string
	stats, err := preview.DecodeModel(ref.Path, data)
	if err != nil {
		side.StatsError = strings.TrimPrefix(err.Error(), "preview: ")
	} else {
		side.Stats = toModelStats(stats)
		deps = stats.Dependencies
	}
	budget := maxRenderBytes - len(data)
	files := map[string][]byte{}
	seen := map[string]bool{}
	i := 0
	for ; i < len(deps) && budget >= 0 && len(files)+len(side.Missing) < maxModelFiles; i++ {
		dep := deps[i]
		if seen[dep] {
			continue
		}
		seen[dep] = true
		full, ok := resolveModelPath(ref.Path, dep)
		if !ok {
			side.Missing = append(side.Missing, dep)
			continue
		}
		depRef := blobRef(&ImageRef{Source: ref.Source, Revision: ref.Revision, Path: full})
		_, content, err := s.readBlob(ctx, client, root, depRef, int64(max(budget, 0))+1)
		switch {
		case errors.Is(err, errBlobMissing):
			side.Missing = append(side.Missing, dep)
			continue
		case errors.Is(err, errBlobTooLarge):
			budget = -1
			continue
		case err != nil:
			return nil, err
		}
		budget -= len(content)
		files[dep] = content
		if strings.EqualFold(path.Ext(dep), ".mtl") {
			for _, tex := range preview.MaterialLibraryDependencies(content) {
				deps = append(deps, path.Join(path.Dir(dep), tex))
			}
		}
	}
	if budget < 0 {
		side.TooLarge = true
		return side, nil
	}
	for _, dep := range deps[i:] {
		if !seen[dep] {
			seen[dep] = true
			side.Missing = append(side.Missing, dep)
		}
	}
	side.Data = base64.StdEncoding.EncodeToString(data)
	for name, content := range files {
		side.Files[name] = base64.StdEncoding.EncodeToString(content)
	}
	return side, nil
}

func modelFormat(p string) string { return strings.TrimPrefix(strings.ToLower(path.Ext(p)), ".") }

// resolveModelPath joins a referenced file onto the model's folder and
// refuses anything that leaves the repository.
func resolveModelPath(model, dep string) (string, bool) {
	if dep == "" || path.IsAbs(dep) || strings.Contains(dep, "\\") {
		return "", false
	}
	full := path.Join(path.Dir(model), dep)
	if full == "." || full == ".." || strings.HasPrefix(full, "../") || strings.HasPrefix(full, "/") {
		return "", false
	}
	return full, true
}

func toModelStats(s *preview.ModelStats) *ModelStats {
	out := &ModelStats{
		Generator: s.Generator, Meshes: s.Meshes, Primitives: s.Primitives, Vertices: s.Vertices, Triangles: s.Triangles,
		Materials: s.Materials, Textures: s.Textures, Nodes: s.Nodes, Skins: s.Skins, Joints: s.Joints,
		Animations: s.Animations, MorphTargets: s.MorphTargets,
	}
	if s.HasBounds {
		out.Min, out.Max = s.Min[:], s.Max[:]
	}
	return out
}

// readBlob reads one file version into memory, refusing more than limit
// bytes.
func (s *Service) readBlob(ctx context.Context, client daemonpb.NipaDaemonClient, root string, ref *daemonpb.BlobRef, limit int64) (*daemonpb.BlobInfo, []byte, error) {
	stream, err := client.ReadBlob(ctx, &daemonpb.ReadBlobRequest{Root: root, Blob: ref})
	if err != nil {
		s.dropIfDisconnected(err)
		return nil, nil, cleanError(err)
	}
	var info *daemonpb.BlobInfo
	var buf bytes.Buffer
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return info, buf.Bytes(), nil
		}
		if err != nil {
			s.dropIfDisconnected(err)
			return nil, nil, cleanError(err)
		}
		switch {
		case ev.GetFailure() != nil:
			if ev.GetFailure().GetCode() == 404 {
				return nil, nil, errBlobMissing
			}
			return nil, nil, errors.New(ev.GetFailure().GetMessage())
		case ev.GetInfo() != nil:
			info = ev.GetInfo()
			if info.GetSizeBytes() > limit {
				return info, nil, errBlobTooLarge
			}
		default:
			if int64(buf.Len()+len(ev.GetData())) > limit {
				return info, nil, errBlobTooLarge
			}
			buf.Write(ev.GetData())
		}
	}
}
