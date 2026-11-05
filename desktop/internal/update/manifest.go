package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Manifest is the Wails update manifest (schemaVersion 1) published as
// update.json next to the release artifacts.
type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	Version       string            `json:"version"`
	Name          string            `json:"name,omitempty"`
	Notes         string            `json:"notes,omitempty"`
	PublishedAt   string            `json:"publishedAt,omitempty"`
	Artifacts     []Artifact        `json:"artifacts"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type Artifact struct {
	URL           string `json:"url"`
	Filename      string `json:"filename"`
	Size          int64  `json:"size"`
	Platform      string `json:"platform"`
	Arch          string `json:"arch,omitempty"`
	DigestAlgo    string `json:"digestAlgo"`
	Digest        string `json:"digest"`
	SignatureAlgo string `json:"signatureAlgo"`
	Signature     string `json:"signature"`
}

// Input names one artifact file; Arch is empty for universal builds.
type Input struct {
	Platform string
	Arch     string
	Path     string
}

// ParseInput reads "platform[/arch]=path".
func ParseInput(spec string) (Input, error) {
	target, path, ok := strings.Cut(spec, "=")
	if !ok || target == "" || path == "" {
		return Input{}, fmt.Errorf("artifact %q: want platform[/arch]=path", spec)
	}
	platform, arch, _ := strings.Cut(target, "/")
	return Input{Platform: platform, Arch: arch, Path: path}, nil
}

type ManifestOptions struct {
	Version string
	Notes   string
	// BaseURL is where the artifacts are downloaded from; each artifact URL
	// is BaseURL + "/" + file name.
	BaseURL string
	PageURL string
	Key     ed25519.PrivateKey
	Now     time.Time
}

// BuildManifest hashes and signs every artifact. The signature is raw
// ed25519 over the SHA-256 digest, which is what the Wails updater checks
// against the pinned public key.
func BuildManifest(opts ManifestOptions, inputs []Input) (Manifest, error) {
	version := Canonical(opts.Version)
	if version == "" {
		return Manifest{}, fmt.Errorf("%q is not a release version", opts.Version)
	}
	if len(opts.Key) != ed25519.PrivateKeySize {
		return Manifest{}, errors.New("an ed25519 private key is required")
	}
	if _, err := url.Parse(opts.BaseURL); err != nil || opts.BaseURL == "" {
		return Manifest{}, fmt.Errorf("invalid base URL %q", opts.BaseURL)
	}
	if len(inputs) == 0 {
		return Manifest{}, errors.New("no artifacts")
	}
	m := Manifest{
		SchemaVersion: 1,
		Version:       version,
		Name:          "Nipa " + version,
		Notes:         opts.Notes,
		PublishedAt:   opts.Now.UTC().Format(time.RFC3339),
	}
	if opts.PageURL != "" {
		m.Metadata = map[string]string{"page": opts.PageURL}
	}
	base := strings.TrimSuffix(opts.BaseURL, "/")
	for _, in := range inputs {
		digest, size, err := digestFile(in.Path)
		if err != nil {
			return Manifest{}, err
		}
		name := filepath.Base(in.Path)
		m.Artifacts = append(m.Artifacts, Artifact{
			URL:           base + "/" + url.PathEscape(name),
			Filename:      name,
			Size:          size,
			Platform:      in.Platform,
			Arch:          in.Arch,
			DigestAlgo:    "sha256",
			Digest:        base64.StdEncoding.EncodeToString(digest),
			SignatureAlgo: "ed25519",
			Signature:     base64.StdEncoding.EncodeToString(ed25519.Sign(opts.Key, digest)),
		})
	}
	return m, nil
}

func digestFile(path string) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return nil, 0, err
	}
	return h.Sum(nil), n, nil
}

// ParsePrivateKey accepts a base64 ed25519 seed (32 bytes) or private key
// (64 bytes).
func ParsePrivateKey(encoded string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("signing key is not base64: %w", err)
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	}
	return nil, fmt.Errorf("signing key has %d bytes, want %d or %d", len(raw), ed25519.SeedSize, ed25519.PrivateKeySize)
}
