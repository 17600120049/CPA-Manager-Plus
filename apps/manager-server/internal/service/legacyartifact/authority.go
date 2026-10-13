package legacyartifact

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
)

var (
	ErrInvalidAuthorityKey = errors.New("authority key must be at least 32 bytes")
	ErrEmptyPayload        = errors.New("cannot sign empty payload")
)

// authority provides package-private HMAC-SHA256 signing and verification for
// foundation checks. A key alone does not establish a qualified Manager owner.
type authority struct {
	key []byte
}

// generateAuthorityKey generates 32 cryptographically secure random bytes
// suitable for operation-scoped signing.
func generateAuthorityKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// newAuthority creates an authority with the given private secret key.
func newAuthority(secret []byte) (*authority, error) {
	if len(secret) < 32 {
		return nil, ErrInvalidAuthorityKey
	}
	k := make([]byte, len(secret))
	copy(k, secret)
	return &authority{key: k}, nil
}

// sign signs the canonical bytes with HMAC-SHA256 and returns a hex string.
func (a *authority) sign(canonicalBytes []byte) (string, error) {
	if a == nil || len(a.key) == 0 {
		return "", ErrInvalidAuthorityKey
	}
	if len(canonicalBytes) == 0 {
		return "", ErrEmptyPayload
	}
	mac := hmac.New(sha256.New, a.key)
	mac.Write(canonicalBytes)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// Verify verifies the HMAC-SHA256 signature in constant time.
func (a *authority) Verify(canonicalBytes []byte, signatureHex string) bool {
	if a == nil || len(a.key) == 0 || len(canonicalBytes) == 0 {
		return false
	}
	sig, err := hex.DecodeString(signatureHex)
	if err != nil || len(sig) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, a.key)
	mac.Write(canonicalBytes)
	expected := mac.Sum(nil)
	return subtle.ConstantTimeCompare(sig, expected) == 1
}
