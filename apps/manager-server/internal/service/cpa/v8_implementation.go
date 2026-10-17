package cpa

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

//go:embed v8.go
var v8SafeReadImplementation []byte

// V8SafeReadImplementationSHA256 binds the Fresh consumer's acceptance to the
// actual compiled safe-read implementation. It grants no qualification itself.
func V8SafeReadImplementationSHA256() string {
	digest := sha256.Sum256(v8SafeReadImplementation)
	return hex.EncodeToString(digest[:])
}
