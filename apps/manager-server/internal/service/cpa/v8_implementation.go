package cpa

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
)

//go:embed v8.go v8_adoption.go
var v8SafeReadImplementation embed.FS

// V8SafeReadImplementationSHA256 binds the Fresh consumer's acceptance to the
// actual compiled safe-read implementation. It grants no qualification itself.
func V8SafeReadImplementationSHA256() string {
	hash := sha256.New()
	for _, name := range []string{"v8.go", "v8_adoption.go"} {
		data, _ := v8SafeReadImplementation.ReadFile(name)
		_, _ = hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
