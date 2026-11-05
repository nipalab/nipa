package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{7}, 64)))
	require.NoError(t, err)
	return pub, priv
}

func writeArtifact(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestParseInput(t *testing.T) {
	in, err := ParseInput("windows/amd64=bin/nipa-desktop.zip")
	require.NoError(t, err)
	require.Equal(t, Input{Platform: "windows", Arch: "amd64", Path: "bin/nipa-desktop.zip"}, in)

	in, err = ParseInput("darwin=Nipa.zip")
	require.NoError(t, err)
	require.Equal(t, Input{Platform: "darwin", Path: "Nipa.zip"}, in)

	for _, bad := range []string{"", "darwin", "=x", "darwin="} {
		_, err := ParseInput(bad)
		require.Error(t, err, bad)
	}
}

func TestBuildManifestSignsDigests(t *testing.T) {
	dir := t.TempDir()
	pub, priv := testKey(t)
	mac := writeArtifact(t, dir, "nipa-desktop-macos.zip", "mac bytes")
	win := writeArtifact(t, dir, "nipa desktop.zip", "windows bytes")

	m, err := BuildManifest(ManifestOptions{
		Version: "v0.2.0", Notes: "fixes", BaseURL: "https://dl.example/v0.2.0/", PageURL: "https://example/rel",
		Key: priv, Now: time.Date(2026, 11, 5, 10, 0, 0, 0, time.FixedZone("x", 3600)),
	}, []Input{{Platform: "darwin", Path: mac}, {Platform: "windows", Arch: "amd64", Path: win}})
	require.NoError(t, err)

	require.Equal(t, 1, m.SchemaVersion)
	require.Equal(t, "0.2.0", m.Version)
	require.Equal(t, "Nipa 0.2.0", m.Name)
	require.Equal(t, "fixes", m.Notes)
	require.Equal(t, "2026-11-05T09:00:00Z", m.PublishedAt)
	require.Equal(t, map[string]string{"page": "https://example/rel"}, m.Metadata)
	require.Len(t, m.Artifacts, 2)

	a := m.Artifacts[1]
	require.Equal(t, "https://dl.example/v0.2.0/nipa%20desktop.zip", a.URL)
	require.Equal(t, "nipa desktop.zip", a.Filename)
	require.Equal(t, int64(len("windows bytes")), a.Size)
	require.Equal(t, "windows", a.Platform)
	require.Equal(t, "amd64", a.Arch)
	require.Equal(t, "sha256", a.DigestAlgo)
	require.Equal(t, "ed25519", a.SignatureAlgo)

	digest := sha256.Sum256([]byte("windows bytes"))
	gotDigest, err := base64.StdEncoding.DecodeString(a.Digest)
	require.NoError(t, err)
	require.Equal(t, digest[:], gotDigest)
	sig, err := base64.StdEncoding.DecodeString(a.Signature)
	require.NoError(t, err)
	require.True(t, ed25519.Verify(pub, digest[:], sig))
	require.Empty(t, m.Artifacts[0].Arch)
}

func TestBuildManifestRejects(t *testing.T) {
	_, priv := testKey(t)
	path := writeArtifact(t, t.TempDir(), "a.zip", "x")
	ok := ManifestOptions{Version: "1.0.0", BaseURL: "https://x", Key: priv}
	inputs := []Input{{Platform: "linux", Path: path}}

	cases := map[string]func(o *ManifestOptions, in *[]Input){
		"not a release version": func(o *ManifestOptions, _ *[]Input) { o.Version = "dev-1" },
		"private key":           func(o *ManifestOptions, _ *[]Input) { o.Key = nil },
		"invalid base URL":      func(o *ManifestOptions, _ *[]Input) { o.BaseURL = "" },
		"no artifacts":          func(_ *ManifestOptions, in *[]Input) { *in = nil },
		"no such file":          func(_ *ManifestOptions, in *[]Input) { *in = []Input{{Platform: "linux", Path: path + ".missing"}} },
	}
	for want, change := range cases {
		o, in := ok, append([]Input(nil), inputs...)
		change(&o, &in)
		_, err := BuildManifest(o, in)
		require.ErrorContains(t, err, want)
	}
}

func TestParsePrivateKey(t *testing.T) {
	_, priv := testKey(t)
	fromSeed, err := ParsePrivateKey(base64.StdEncoding.EncodeToString(priv.Seed()) + "\n")
	require.NoError(t, err)
	require.Equal(t, priv, fromSeed)
	full, err := ParsePrivateKey(base64.StdEncoding.EncodeToString(priv))
	require.NoError(t, err)
	require.Equal(t, priv, full)

	_, err = ParsePrivateKey("***")
	require.ErrorContains(t, err, "not base64")
	_, err = ParsePrivateKey(base64.StdEncoding.EncodeToString([]byte("short")))
	require.ErrorContains(t, err, "has 5 bytes")
}

func TestManifestCommand(t *testing.T) {
	dir := t.TempDir()
	_, priv := testKey(t)
	path := writeArtifact(t, dir, "nipa-desktop-linux-amd64.tar.gz", "linux")
	notes := writeArtifact(t, dir, "notes.md", "* faster clones")
	env := map[string]string{EnvSigningKey: base64.StdEncoding.EncodeToString(priv.Seed())}
	getenv := func(k string) string { return env[k] }

	var out bytes.Buffer
	require.NoError(t, ManifestCommand([]string{"-version", "v1.0.0", "-base", "https://dl", "-notes", notes, "linux/amd64=" + path}, getenv, &out))
	var m Manifest
	require.NoError(t, json.Unmarshal(out.Bytes(), &m))
	require.Equal(t, "1.0.0", m.Version)
	require.Equal(t, "* faster clones", m.Notes)
	require.Equal(t, "https://dl/nipa-desktop-linux-amd64.tar.gz", m.Artifacts[0].URL)

	file := filepath.Join(dir, "update.json")
	require.NoError(t, ManifestCommand([]string{"-version", "1.0.0", "-base", "https://dl", "-out", file, "linux=" + path}, getenv, &out))
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	require.True(t, json.Valid(data))

	require.ErrorContains(t, ManifestCommand([]string{"-version", "1.0.0"}, func(string) string { return "" }, &out), "is not set")
	require.ErrorContains(t, ManifestCommand([]string{"-version", "1.0.0", "bad"}, getenv, &out), "platform[/arch]=path")
	require.Error(t, ManifestCommand([]string{"-version", "1.0.0", "-notes", filepath.Join(dir, "none"), "linux=" + path}, getenv, &out))
	require.Error(t, ManifestCommand([]string{"-nope"}, getenv, &out))
	require.ErrorContains(t, ManifestCommand(nil, func(string) string { return "!!" }, &out), "not base64")
	require.ErrorContains(t, ManifestCommand([]string{"-version", "1.0.0", "-base", "https://dl"}, getenv, &out), "no artifacts")
}

func TestManifestCommandGenkey(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, ManifestCommand([]string{"-genkey"}, nil, &out))
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 2)
	pub, err := base64.StdEncoding.DecodeString(strings.Fields(lines[0])[1])
	require.NoError(t, err)
	require.Len(t, pub, ed25519.PublicKeySize)
	priv, err := ParsePrivateKey(strings.Fields(lines[1])[1])
	require.NoError(t, err)
	require.Equal(t, ed25519.PublicKey(pub), priv.Public())
}
