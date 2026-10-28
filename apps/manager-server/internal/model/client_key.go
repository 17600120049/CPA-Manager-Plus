package model

import "strings"

// The Manager mints exactly two canonical Client Key shapes. A workspace key is
// created by the Key workspace; the install key is generated once by a Fresh
// New Managed installation so CPA never starts with client authentication
// disabled. Both are real CPA client credentials, so the same shape rules bind
// the Manager, the Runtime destination and every CPA read-back.
const (
	// WorkspaceClientKeyPrefix introduces a Key workspace secret.
	WorkspaceClientKeyPrefix = "cpamp_sk_"
	// WorkspaceClientKeyLength is the prefix plus 64 lowercase hex characters.
	WorkspaceClientKeyLength = len(WorkspaceClientKeyPrefix) + 64
	// InitialClientKeyPrefix introduces the install-generated secret, matching
	// the v1.x default-install key.
	InitialClientKeyPrefix = "sk-"
	// InitialClientKeyRandomLength is the number of [A-Za-z0-9] characters the
	// install key carries after its prefix.
	InitialClientKeyRandomLength = 64
	// InitialClientKeyLength is the complete install-generated secret length.
	InitialClientKeyLength = len(InitialClientKeyPrefix) + InitialClientKeyRandomLength
	// MaskedClientKeyEllipsis separates a safe prefix from the last characters.
	MaskedClientKeyEllipsis = "…"
)

// ValidInitialClientKey reports whether secret is the install-generated Client
// Key shape: "sk-" followed by exactly 64 [A-Za-z0-9] characters.
func ValidInitialClientKey(secret string) bool {
	if len(secret) != InitialClientKeyLength || !strings.HasPrefix(secret, InitialClientKeyPrefix) {
		return false
	}
	for _, character := range secret[len(InitialClientKeyPrefix):] {
		if !isASCIIAlnum(character) {
			return false
		}
	}
	return true
}

// ValidWorkspaceClientKey reports whether secret is a Key workspace secret:
// "cpamp_sk_" followed by exactly 64 lowercase hex characters.
func ValidWorkspaceClientKey(secret string) bool {
	if len(secret) != WorkspaceClientKeyLength || !strings.HasPrefix(secret, WorkspaceClientKeyPrefix) {
		return false
	}
	for _, character := range secret[len(WorkspaceClientKeyPrefix):] {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

// ValidCanonicalClientKey reports whether secret is one of the two shapes the
// Manager persists as a canonical Client Key.
func ValidCanonicalClientKey(secret string) bool {
	return ValidWorkspaceClientKey(secret) || ValidInitialClientKey(secret)
}

// MaskedClientKey is the only Client Key rendering that may leave the Manager:
// the shape-owning prefix, an ellipsis and the last four characters.
func MaskedClientKey(secret string) string {
	if !ValidCanonicalClientKey(secret) {
		return ""
	}
	prefix := WorkspaceClientKeyPrefix
	if ValidInitialClientKey(secret) {
		prefix = InitialClientKeyPrefix
	}
	return prefix + MaskedClientKeyEllipsis + secret[len(secret)-4:]
}

func isASCIIAlnum(character rune) bool {
	return character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}
