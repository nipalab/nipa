package domain

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseNipaUrl_Valid(t *testing.T) {
	tests := []struct {
		name      string
		urlString string
		want      *NipaUrl
	}{
		{
			name:      "https with port",
			urlString: "https://example.com:9000/org/project",
			want: &NipaUrl{
				Url:     "https://example.com:9000/org/project",
				Host:    "example.com:9000",
				Origin:  "https://example.com:9000",
				Org:     "org",
				Project: "project",
				Path:    "",
			},
		},
		{
			name:      "http without port",
			urlString: "http://example.com/org/project",
			want: &NipaUrl{
				Url:     "http://example.com/org/project",
				Host:    "example.com",
				Origin:  "http://example.com",
				Org:     "org",
				Project: "project",
				Path:    "",
			},
		},
		{
			name:      "https with repo path",
			urlString: "https://example.com/org/project/path/to/repo",
			want: &NipaUrl{
				Url:     "https://example.com/org/project/path/to/repo",
				Host:    "example.com",
				Origin:  "https://example.com",
				Org:     "org",
				Project: "project",
				Path:    "path/to/repo",
			},
		},
		{
			name:      "https with single path segment",
			urlString: "https://example.com/org/project/subdir",
			want: &NipaUrl{
				Url:     "https://example.com/org/project/subdir",
				Host:    "example.com",
				Origin:  "https://example.com",
				Org:     "org",
				Project: "project",
				Path:    "subdir",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNipaUrl(tt.urlString)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseNipaUrl_InvalidScheme(t *testing.T) {
	_, err := ParseNipaUrl("ftp://example.com/org/project")
	require.Error(t, err)
	require.EqualError(t, err, "invalid URL scheme, expected http or https")

	var domainErr *Error
	require.True(t, errors.As(err, &domainErr))
	require.Equal(t, 400, domainErr.Code)
}

func TestParseNipaUrl_InvalidURL(t *testing.T) {
	_, err := ParseNipaUrl("://bad url")
	require.Error(t, err)
	require.Contains(t, err.Error(), "url can not be parsed")
}

func TestParseNipaUrl_InvalidPath(t *testing.T) {
	_, err := ParseNipaUrl("https://example.com/org")
	require.Error(t, err)
	require.EqualError(t, err, "invalid URL path, expected /org/project/[path]")

	_, err = ParseNipaUrl("https://example.com")
	require.Error(t, err)
	require.EqualError(t, err, "invalid URL path, expected /org/project/[path]")
}