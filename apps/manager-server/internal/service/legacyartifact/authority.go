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

// Authority provides operation-scoped HMAC-SHA256 signing and verification
// owned by the Manager. It prevents untrusted JSON receipts, forging, and tampering.
type Authority struct {
	key []byte
}

// GenerateAuthorityKey generates 32 cryptographically secure random bytes
// suitable for operation-scoped signing.
func GenerateAuthorityKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// NewAuthority creates an Authority with the given private secret key.
func NewAuthority(secret []byte) (*Authority, error) {
	if len(secret) < 32 {
		return nil, ErrInvalidAuthorityKey
	}
	k := make([]byte, len(secret))
	copy(k, secret)
	return &Authority{key: k}, nil
}

// Sign signs the canonical bytes with HMAC-SHA256 and returns a hex string.
func (a *Authority) Sign(canonicalBytes []byte) (string, error) {
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
func (a *Authority) Verify(canonicalBytes []byte, signatureHex string) bool {
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
