// Package output renders client results as machine-readable JSON for the CLI
// --json mode and for programmatic consumers.
package output

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

const EnvFormat = "NIPA_OUTPUT"

func FromEnv() bool {
	return strings.EqualFold(os.Getenv(EnvFormat), "json")
}

func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Action  string `json:"action,omitempty"`
}

func WriteError(w io.Writer, err error) error {
	body := ErrorBody{Code: 1, Message: err.Error()}
	var domErr *clientDomain.Error
	if errors.As(err, &domErr) {
		body.Code = domErr.Code
		body.Message = domErr.Message
		body.Hint = domErr.Hint
		body.Action = domErr.Action
	}
	return WriteJSON(w, ErrorEnvelope{Error: body})
}
