package oauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
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
	Binding   string `json:"b"`
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
// (0 = anonymous login/signup flow). The binding is a random secret the starting
// browser keeps in a cookie; only its hash travels through the provider, so a
// state forwarded to a different browser cannot complete the callback.
func (s *stateSigner) Sign(redirect string, linkUser int64, binding string) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", domain.NewErrorInternalServer(err.Error())
	}
	data := stateData{
		Nonce:     base64.RawURLEncoding.EncodeToString(nonce),
		Redirect:  safeRedirect(redirect),
		LinkUser:  linkUser,
		Binding:   bindingHash(binding),
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

// bound reports whether the browser presenting binding is the one that started
// the flow.
func (d *stateData) bound(binding string) bool {
	if binding == "" || d.Binding == "" {
		return false
	}
	return hmac.Equal([]byte(d.Binding), []byte(bindingHash(binding)))
}

func bindingHash(binding string) string {
	if binding == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(binding))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func newBinding() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", domain.NewErrorInternalServer(err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *stateSigner) signature(encoded string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// safeRedirect only accepts relative SPA paths, so a forged state can never
// turn the callback into an open redirect. Besides absolute URLs and
// protocol-relative targets ("//host"), backslashes and ASCII control
// characters are rejected: browsers normalize "\" to "/" and strip tabs, so
// "/\evil.com" or "/\t/evil.com" would otherwise resolve off-site.
func safeRedirect(target string) string {
	target = strings.TrimSpace(target)
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Opaque != "" ||
		!strings.HasPrefix(parsed.Path, "/") || strings.Contains(target, `\`) {
		return "/"
	}
	return target
}

func errInvalidState() error {
	return domain.NewErrorUser("invalid oauth state")
}
