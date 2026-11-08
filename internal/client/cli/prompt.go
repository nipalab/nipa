package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

type Prompter struct {
	reader *bufio.Reader
}

func NewPrompter() *Prompter {
	return &Prompter{
		reader: bufio.NewReader(os.Stdin),
	}
}

func (p *Prompter) PromptUsername() (string, error) {
	if _, err := fmt.Fprint(os.Stdout, "Username: "); err != nil {
		return "", err
	}
	username, err := p.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(username), nil
}

func (p *Prompter) PromptPassword() (string, error) {
	if _, err := fmt.Fprint(os.Stdout, "Password: "); err != nil {
		return "", err
	}
	password, err := p.readSecret()
	if _, werr := fmt.Fprintln(os.Stdout); werr != nil {
		return "", werr
	}
	return password, err
}

func (p *Prompter) PromptPassphrase(keyPath string) (string, error) {
	if _, err := fmt.Fprintf(os.Stdout, "Passphrase for %s: ", keyPath); err != nil {
		return "", err
	}
	passphrase, err := p.readSecret()
	if _, werr := fmt.Fprintln(os.Stdout); werr != nil {
		return "", werr
	}
	return passphrase, err
}

func (p *Prompter) PromptUsernameAndPassword() (username, password string, err error) {
	username, err = p.PromptUsername()
	if err != nil {
		return "", "", err
	}
	password, err = p.PromptPassword()
	if err != nil {
		return "", "", err
	}
	return username, password, nil
}

func (p *Prompter) readSecret() (string, error) {
	secret, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	return string(secret), nil
}

// confirm asks a yes/no question on out and reads one answer line from in.
func confirm(in io.Reader, out io.Writer, question string) (bool, error) {
	if _, err := fmt.Fprint(out, question); err != nil {
		return false, err
	}
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
