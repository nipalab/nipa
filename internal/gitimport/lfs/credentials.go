package lfs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

type Credentials struct {
	Username   string
	Password   string
	AuthType   string
	Credential string
}

func (c Credentials) Empty() bool {
	return c.Username == "" && c.Password == "" && c.Credential == ""
}

func (c Credentials) Header() string {
	if c.AuthType != "" && c.Credential != "" {
		return c.AuthType + " " + c.Credential
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password))
}

type CredentialHelper interface {
	Fill(ctx context.Context, u *url.URL) (Credentials, error)
	Approve(ctx context.Context, u *url.URL, c Credentials) error
	Reject(ctx context.Context, u *url.URL, c Credentials) error
}

type CommandRunner func(ctx context.Context, stdin string, args ...string) (string, error)

type GitCredentials struct {
	Run CommandRunner
}

func NewGitCredentials() CredentialHelper {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil
	}
	return &GitCredentials{Run: gitRunner(bin)}
}

func gitRunner(bin string) CommandRunner {
	return func(ctx context.Context, stdin string, args ...string) (string, error) {
		return runCommand(ctx, bin, stdin, args...)
	}
}

func runCommand(ctx context.Context, bin, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stderr = os.Stderr
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.String(), err
}

var ErrNoCredentials = errors.New("no credentials")

func (g *GitCredentials) Fill(ctx context.Context, u *url.URL) (Credentials, error) {
	out, err := g.Run(ctx, describe(u, Credentials{})+"capability[]=authtype\n\n", "credential", "fill")
	if err != nil {
		return Credentials{}, ErrNoCredentials
	}
	var c Credentials
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "username":
			c.Username = value
		case "password":
			c.Password = value
		case "authtype":
			c.AuthType = value
		case "credential":
			c.Credential = value
		}
	}
	if c.Empty() {
		return Credentials{}, ErrNoCredentials
	}
	return c, nil
}

func (g *GitCredentials) Approve(ctx context.Context, u *url.URL, c Credentials) error {
	_, err := g.Run(ctx, describe(u, c)+"\n", "credential", "approve")
	return err
}

func (g *GitCredentials) Reject(ctx context.Context, u *url.URL, c Credentials) error {
	_, err := g.Run(ctx, describe(u, c)+"\n", "credential", "reject")
	return err
}

func describe(u *url.URL, c Credentials) string {
	var b strings.Builder
	b.WriteString("protocol=" + u.Scheme + "\n")
	b.WriteString("host=" + u.Host + "\n")
	if c.AuthType != "" {
		b.WriteString("capability[]=authtype\nauthtype=" + c.AuthType + "\ncredential=" + c.Credential + "\n")
		return b.String()
	}
	if c.Username != "" {
		b.WriteString("username=" + c.Username + "\n")
	}
	if c.Password != "" {
		b.WriteString("password=" + c.Password + "\n")
	}
	return b.String()
}

func FromURL(u *url.URL) (Credentials, bool) {
	if u.User == nil {
		return Credentials{}, false
	}
	pass, _ := u.User.Password()
	c := Credentials{Username: u.User.Username(), Password: pass}
	return c, !c.Empty()
}

func StripUserinfo(u *url.URL) *url.URL {
	out := *u
	out.User = nil
	return &out
}

func SameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(hostPort(a), hostPort(b))
}

func hostPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return u.Hostname() + ":443"
	case "http":
		return u.Hostname() + ":80"
	}
	return u.Host
}
