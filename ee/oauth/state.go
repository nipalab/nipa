package oauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
)

// stateTTL bounds how long an authorization round-trip may take.
const stateTTL = 10 * time.Minute

// stateData travels through the OAuth provider as the signed state parameter:
// it carries the SPA path to return to and, for account linking, the nipad user
// the identity must be attached to.
type stateData struct {
	Nonce     string `json:"n"`
	Redirect  string `json:"r"`
	LinkUser  int64  `json:"u,omitempty"`
	ExpiresAt int64  `json:"e"`
}

type stateSigner struct {
	secret []byte
	now    func() time.Time
}

func newStateSigner(secret string) *stateSigner {
	return &stateSigner{secret: []byte(secret), now: time.Now}
}

// Sign builds a signed state for the given SPA redirect and optional link user
// (0 = anonymous login/signup flow).
func (s *stateSigner) Sign(redirect string, linkUser int64) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", domain.NewErrorInternalServer(err.Error())
	}
	data := stateData{
		Nonce:     base64.RawURLEncoding.EncodeToString(nonce),
		Redirect:  safeRedirect(redirect),
		LinkUser:  linkUser,
		ExpiresAt: s.now().Add(stateTTL).Unix(),
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return "", domain.NewErrorInternalServer(err.Error())
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + s.signature(encoded), nil
}

// Verify checks the signature and expiry and returns the state payload.
func (s *stateSigner) Verify(raw string) (*stateData, error) {
	encoded, sig, ok := strings.Cut(raw, ".")
	if !ok || encoded == "" || sig == "" {
		return nil, errInvalidState()
	}
	if !hmac.Equal([]byte(sig), []byte(s.signature(encoded))) {
		return nil, errInvalidState()
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errInvalidState()
	}
	var data stateData
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, errInvalidState()
	}
	if data.ExpiresAt < s.now().Unix() {
		return nil, errInvalidState()
	}
	data.Redirect = safeRedirect(data.Redirect)
	return &data, nil
}

func (s *stateSigner) signature(encoded string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// safeRedirect only accepts relative SPA paths, so a forged state can never
// turn the callback into an open redirect.
func safeRedirect(target string) string {
	target = strings.TrimSpace(target)
	if !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") {
		return "/"
	}
	return target
}

func errInvalidState() error {
	return domain.NewErrorUser("invalid oauth state")
}
