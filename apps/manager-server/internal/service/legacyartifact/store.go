package legacyartifact

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrInsecureKeyFile = errors.New("authority key file is insecure (symlink or non-regular file)")
)

// DefaultAuthorityKeyPath returns the canonical path to the Manager-owned private authority key.
func DefaultAuthorityKeyPath(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data"
	}
	return filepath.Join(dataDir, ".legacy-authority.key")
}

// DefaultJournalPath returns the canonical path to the Manager-owned operation journal.
func DefaultJournalPath(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data"
	}
	return filepath.Join(dataDir, "legacy-hop-journal.json")
}

// LoadOrCreateAuthority loads the private HMAC key from keyPath, or generates and stores
// a new 32-byte cryptographically secure key with 0600 permissions in a 0700 directory.
// Symlinks and non-regular files are rejected to prevent redirection attacks.
func LoadOrCreateAuthority(keyPath string) (*Authority, error) {
	keyPath = strings.TrimSpace(keyPath)
	if keyPath == "" {
		return nil, errors.New("authority key path is required")
	}

	info, err := os.Lstat(keyPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, ErrInsecureKeyFile
		}
		data, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read authority key %s: %w", keyPath, err)
		}
		raw := strings.TrimSpace(string(data))
		// Try hex decoding first if it looks like a hex string
		if len(raw) >= 64 {
			if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) >= 32 {
				return NewAuthority(decoded)
			}
		}
		// Fallback to raw bytes
		if len(data) >= 32 {
			return NewAuthority(data[:32])
		}
		return nil, ErrInvalidAuthorityKey
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect authority key %s: %w", keyPath, err)
	}

	// Key file does not exist, generate new 32-byte key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate authority key: %w", err)
	}

	dir := filepath.Dir(keyPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create authority directory %s: %w", dir, err)
	}

	content := []byte(hex.EncodeToString(key) + "\n")
	tmpPath := fmt.Sprintf("%s.tmp.%d", keyPath, os.Getpid())
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create temporary authority file %s: %w", tmpPath, err)
	}
	if _, err := tmpFile.Write(content); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("write temporary authority file %s: %w", tmpPath, err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("sync temporary authority file %s: %w", tmpPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("close temporary authority file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, keyPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("persist authority file %s: %w", keyPath, err)
	}

	return NewAuthority(key)
}
