package installer

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// GenerateAuthToken returns a URL-safe, 32-byte random token suitable for the
// server.auth_token config field. Replaces the python one-liner in the docs:
//
//	python3 -c "import secrets; print(secrets.token_urlsafe(32))"
func GenerateAuthToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read crypto/rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
