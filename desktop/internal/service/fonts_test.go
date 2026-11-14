package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/nipalab/nipa/internal/preview"
)

func TestService_FontDiffComparesVersions(t *testing.T) {
	svc, _ := newModels(t, map[string]string{"ui/Go.ttf": string(goregular.TTF), "ui/GoMono.ttf": string(gomono.TTF), "ui/Bold.ttf": string(gobold.TTF)})
	out, err := svc.FontDiff(ctx, "/r", &ImageRef{Source: "snapshot", Path: "ui/Go.ttf"}, &ImageRef{Source: "working", Path: "ui/Bold.ttf"})
	require.NoError(t, err)
	require.Equal(t, "Go", out.Old.Info.Family)
	require.Equal(t, "Bold", out.New.Info.Style)
	require.True(t, strings.HasPrefix(out.New.Sheet, "data:image/png;base64,"))
	require.NotEmpty(t, out.Old.Info.Scripts)
	require.NotEmpty(t, out.Old.Info.Ranges)
	require.NotNil(t, out.Changes)
	names := map[string]bool{}
	for _, m := range out.Changes.Metrics {
		names[m.Name] = true
	}
	require.True(t, names["Style"])

	out, err = svc.FontDiff(ctx, "/r", &ImageRef{Source: "snapshot", Path: "ui/Go.ttf"}, &ImageRef{Source: "working", Path: "ui/GoMono.ttf"})
	require.NoError(t, err)
	require.Equal(t, out.Old.Info.Codepoints+out.Changes.AddedCount-out.Changes.RemovedCount, out.New.Info.Codepoints)
}

func TestService_FontDiffSingleSideAndErrors(t *testing.T) {
	svc, d := newModels(t, map[string]string{"a.ttf": "nope", "b.woff2": "wOF2....", "big.otf": "x", "ok.ttf": string(goregular.TTF)})
	d.sizes["big.otf"] = preview.MaxFontBytes + 1

	out, err := svc.FontDiff(ctx, "/r", &ImageRef{Source: "snapshot", Path: "a.ttf"}, &ImageRef{Source: "working", Path: "ok.ttf"})
	require.NoError(t, err)
	require.Nil(t, out.Old.Info)
	require.NotEmpty(t, out.Old.Error)
	require.Nil(t, out.Changes)

	out, err = svc.FontDiff(ctx, "/r", nil, &ImageRef{Source: "working", Path: "b.woff2"})
	require.NoError(t, err)
	require.Contains(t, out.New.Error, "WOFF2")

	out, err = svc.FontDiff(ctx, "/r", nil, &ImageRef{Source: "working", Path: "big.otf"})
	require.NoError(t, err)
	require.True(t, out.New.TooLarge)

	_, err = svc.FontDiff(ctx, "/r", nil, nil)
	require.Error(t, err)
	_, err = svc.FontDiff(ctx, "/r", nil, &ImageRef{Source: "working", Path: "gone.ttf"})
	require.Error(t, err)
}

func TestCodeRangesTruncates(t *testing.T) {
	in := make([]preview.RuneRange, maxFontRanges+3)
	out, cut := codeRanges(in)
	require.True(t, cut)
	require.Len(t, out, maxFontRanges)
	out, cut = codeRanges(in[:2])
	require.False(t, cut)
	require.Len(t, out, 2)
}
