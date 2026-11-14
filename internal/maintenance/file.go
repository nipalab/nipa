package maintenance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type fileBody struct {
	Mode    string     `json:"mode"`
	Message string     `json:"message"`
	EndsAt  *time.Time `json:"ends_at"`
}

// ParseFile reads the override file: an empty file means full, otherwise it
// holds {mode, message, ends_at}. The window is active from the zero time.
func ParseFile(data []byte) (Window, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Window{Mode: Full}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var body fileBody
	if err := dec.Decode(&body); err != nil {
		return Window{}, fmt.Errorf("parse maintenance file: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Window{}, fmt.Errorf("parse maintenance file: trailing data")
	}
	mode := Full
	if body.Mode != "" {
		m, err := ParseMode(body.Mode)
		if err != nil {
			return Window{}, fmt.Errorf("parse maintenance file: %w", err)
		}
		mode = m
	}
	return Window{Mode: mode, Message: body.Message, EndsAt: body.EndsAt}, nil
}
