package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

const EnvSigningKey = "NIPA_UPDATE_SIGNING_KEY"

// ManifestCommand is the release tool behind `go run ./tools/updatemanifest`:
//
//	-genkey                      print a new key pair
//	-version v1.2.3 -base URL [-page URL] [-notes file] [-out update.json] platform[/arch]=path...
//
// The private key comes from NIPA_UPDATE_SIGNING_KEY.
func ManifestCommand(args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flag.NewFlagSet("updatemanifest", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	genkey := fs.Bool("genkey", false, "")
	version := fs.String("version", "", "")
	base := fs.String("base", "", "")
	page := fs.String("page", "", "")
	notesFile := fs.String("notes", "", "")
	out := fs.String("out", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *genkey {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "public  %s\nprivate %s\n",
			base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv.Seed()))
		return err
	}
	encoded := getenv(EnvSigningKey)
	if encoded == "" {
		return errors.New(EnvSigningKey + " is not set")
	}
	key, err := ParsePrivateKey(encoded)
	if err != nil {
		return err
	}
	var inputs []Input
	for _, spec := range fs.Args() {
		in, err := ParseInput(spec)
		if err != nil {
			return err
		}
		inputs = append(inputs, in)
	}
	notes := ""
	if *notesFile != "" {
		data, err := os.ReadFile(*notesFile)
		if err != nil {
			return err
		}
		notes = string(data)
	}
	m, err := BuildManifest(ManifestOptions{
		Version: *version, Notes: notes, BaseURL: *base, PageURL: *page, Key: key, Now: time.Now(),
	}, inputs)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if *out == "" {
		_, err = stdout.Write(data)
		return err
	}
	return os.WriteFile(*out, data, 0o644)
}
