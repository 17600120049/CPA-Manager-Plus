package legacyartifact

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	ErrInsecureKeyFile = errors.New("authority key file is insecure (symlink, non-regular file, or excessive permissions)")
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

// LoadAuthority reads an existing private key. Receipt verification must not
// generate a replacement key when its authority has been lost.
func LoadAuthority(keyPath string) (*Authority, error) {
	keyPath = strings.TrimSpace(keyPath)
	if keyPath == "" {
		return nil, errors.New("authority key path is required")
	}
	info, err := os.Lstat(keyPath)
	if err != nil {
		return nil, fmt.Errorf("inspect authority key: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, ErrInsecureKeyFile
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		return nil, ErrInsecureKeyFile
	}
	if err := checkAuthorityDirectory(filepath.Dir(keyPath)); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read authority key: %w", err)
	}
	raw := strings.TrimSpace(string(data))
	if len(raw) >= 64 {
		if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) >= 32 {
			return NewAuthority(decoded)
		}
	}
	if len(data) >= 32 {
		return NewAuthority(data[:32])
	}
	return nil, ErrInvalidAuthorityKey
}

func checkAuthorityDirectory(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
		return ErrInsecureKeyFile
	}
	return nil
}

// LoadOrCreateAuthority serializes initial creation across processes and
// goroutines, rechecks the key under lock, and never replaces an existing key.
func LoadOrCreateAuthority(keyPath string) (*Authority, error) {
	keyPath = strings.TrimSpace(keyPath)
	if keyPath == "" {
		return nil, errors.New("authority key path is required")
	}
	absolute, err := filepath.Abs(keyPath)
	if err != nil {
		return nil, err
	}
	keyPath = absolute
	dir := filepath.Dir(keyPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := checkAuthorityDirectory(dir); err != nil {
		return nil, err
	}
	lock, err := acquireFileLock(keyPath + ".lock")
	if err != nil {
		return nil, fmt.Errorf("lock authority creation: %w", err)
	}
	defer lock.Release()
	authority, err := LoadAuthority(keyPath)
	if err == nil {
		return authority, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".legacy-authority-key-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write([]byte(hex.EncodeToString(key) + "\n")); err != nil {
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp.Name(), keyPath); err != nil {
		return nil, err
	}
	return NewAuthority(key)
}
