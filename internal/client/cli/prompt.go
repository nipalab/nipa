package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/nipalab/nipa/internal/client/usecase"
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

// PromptOTP asks for a two-factor code; without a terminal it fails with the
// hint to log in interactively or with an SSH key.
func (p *Prompter) PromptOTP(attempt int) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", usecase.MFARequiredError()
	}
	if attempt > 0 {
		if _, err := fmt.Fprintln(os.Stdout, "Invalid code, try again."); err != nil {
			return "", err
		}
	}
	if _, err := fmt.Fprint(os.Stdout, "Authentication code: "); err != nil {
		return "", err
	}
	code, err := p.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(code), nil
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
