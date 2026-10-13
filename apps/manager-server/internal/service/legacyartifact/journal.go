package legacyartifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrOperationReplayed  = errors.New("operation already recorded in private journal")
	ErrOperationNotFound  = errors.New("operation not found in private journal")
	ErrCorruptJournal     = errors.New("journal file is corrupt, malformed, or contains duplicate records")
	ErrInsecureJournal    = errors.New("journal file is insecure (symlink or non-regular file)")
	ErrJournalUnavailable = errors.New("private journal is missing or unreadable")
)

type OperationRecord struct {
	OperationID          string `json:"operationId"`
	Release              string `json:"release"`
	Asset                string `json:"asset"`
	ExecutableSHA256     string `json:"executableSHA256"`
	InputManifestDigest  string `json:"inputManifestDigest"`
	OutputManifestDigest string `json:"outputManifestDigest"`
	IssuedAtMS           int64  `json:"issuedAtMs"`
	Revoked              bool   `json:"revoked,omitempty"`
}

// Validate verifies that an OperationRecord contains all critical authority fields.
func (r OperationRecord) Validate() error {
	if strings.TrimSpace(r.OperationID) == "" {
		return errors.New("operationId is required in journal record")
	}
	if strings.TrimSpace(r.Release) == "" {
		return errors.New("release is required in journal record")
	}
	if strings.TrimSpace(r.Asset) == "" {
		return errors.New("asset is required in journal record")
	}
	if strings.TrimSpace(r.ExecutableSHA256) == "" {
		return errors.New("executableSHA256 is required in journal record")
	}
	if strings.TrimSpace(r.InputManifestDigest) == "" {
		return errors.New("inputManifestDigest is required in journal record")
	}
	if strings.TrimSpace(r.OutputManifestDigest) == "" {
		return errors.New("outputManifestDigest is required in journal record")
	}
	if r.IssuedAtMS <= 0 {
		return errors.New("issuedAtMs must be positive in journal record")
	}
	return nil
}

// Journal maintains a private, Manager-owned record of executed forward-hop operations.
// It serves as an anti-replay store to ensure that an operation receipt corresponds
// to an actual operation issued by this authority.
type Journal struct {
	mu      sync.RWMutex
	path    string
	records map[string]OperationRecord
}

// NewMemoryJournal creates an in-memory Journal for transient or testing operations.
func NewMemoryJournal() *Journal {
	return &Journal{
		records: make(map[string]OperationRecord),
	}
}

// OpenJournal opens an existing JSON-backed Journal. Validation never creates
// a missing authority file or restores records from an in-memory cache.
// If the journal exists but is corrupt, malformed, contains duplicate records,
// or is an insecure symlink, it fails closed with an error.
func OpenJournal(path string) (*Journal, error) {
	path = strings.TrimSpace(path)
	j := &Journal{
		path:    path,
		records: make(map[string]OperationRecord),
	}
	if path == "" {
		return nil, ErrJournalRequired
	}

	if err := j.loadFromDiskLocked(); err != nil {
		return nil, err
	}

	return j, nil
}

// OpenOrCreateJournal explicitly initializes a new private journal under the
// same cross-process lock used by writers. Runtime reloads never initialize it.
func OpenOrCreateJournal(path string) (*Journal, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, ErrJournalRequired
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	lock, err := acquireFileLock(path + ".lock")
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	j := &Journal{path: path, records: make(map[string]OperationRecord)}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		if err := j.persistLocked(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, errors.Join(ErrJournalUnavailable, err)
	}
	if err := j.loadFromDiskLocked(); err != nil {
		return nil, err
	}
	return j, nil
}

func (j *Journal) loadFromDiskLocked() (loadErr error) {
	defer func() {
		if loadErr != nil {
			j.records = make(map[string]OperationRecord)
		}
	}()
	if j.path == "" {
		return nil
	}

	info, err := os.Lstat(j.path)
	if err != nil {
		return errors.Join(ErrJournalUnavailable, fmt.Errorf("inspect journal path %s: %w", j.path, err))
	}

	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ErrInsecureJournal
	}

	data, err := os.ReadFile(j.path)
	if err != nil {
		return errors.Join(ErrJournalUnavailable, fmt.Errorf("read journal file %s: %w", j.path, err))
	}

	var list []OperationRecord
	if err := json.Unmarshal(data, &list); err != nil {
		return errors.Join(ErrCorruptJournal, fmt.Errorf("unmarshal journal %s: %w", j.path, err))
	}

	seenIDs := make(map[string]bool, len(list))
	newRecords := make(map[string]OperationRecord, len(list))
	for i, rec := range list {
		if err := rec.Validate(); err != nil {
			return errors.Join(ErrCorruptJournal, fmt.Errorf("record %d invalid: %w", i, err))
		}
		if seenIDs[rec.OperationID] {
			return errors.Join(ErrCorruptJournal, fmt.Errorf("duplicate operation ID %s", rec.OperationID))
		}
		seenIDs[rec.OperationID] = true
		newRecords[rec.OperationID] = rec
	}

	j.records = newRecords
	return nil
}

// Path returns the path of the journal file.
func (j *Journal) Path() string {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.path
}

// RecordOperation records a new completed operation. If the operationID is already present,
// it returns ErrOperationReplayed. Cross-process file locking ensures mutual exclusion.
// Persistence failures cause the entire record operation to fail and not be committed to memory.
func (j *Journal) RecordOperation(rec OperationRecord) error {
	if err := rec.Validate(); err != nil {
		return err
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if j.path != "" {
		lock, err := acquireFileLock(j.path + ".lock")
		if err != nil {
			return fmt.Errorf("acquire journal lock: %w", err)
		}
		defer lock.Release()

		if err := j.loadFromDiskLocked(); err != nil {
			return err
		}
	}

	if _, exists := j.records[rec.OperationID]; exists {
		return ErrOperationReplayed
	}

	j.records[rec.OperationID] = rec
	if err := j.persistLocked(); err != nil {
		delete(j.records, rec.OperationID)
		return err
	}
	return nil
}

// GetOperation returns the record for an operationID if present.
func (j *Journal) GetOperation(operationID string) (OperationRecord, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.path != "" {
		lock, err := acquireFileLock(j.path + ".lock")
		if err != nil {
			return OperationRecord{}, false, errors.Join(ErrJournalUnavailable, err)
		}
		defer lock.Release()
		if err := j.loadFromDiskLocked(); err != nil {
			return OperationRecord{}, false, err
		}
	}

	rec, ok := j.records[operationID]
	return rec, ok, nil
}

// RevokeOperation marks an operation as revoked.
func (j *Journal) RevokeOperation(operationID string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.path != "" {
		lock, err := acquireFileLock(j.path + ".lock")
		if err != nil {
			return fmt.Errorf("acquire journal lock: %w", err)
		}
		defer lock.Release()

		if err := j.loadFromDiskLocked(); err != nil {
			return err
		}
	}

	rec, ok := j.records[operationID]
	if !ok {
		return ErrOperationNotFound
	}
	origRevoked := rec.Revoked
	rec.Revoked = true
	j.records[operationID] = rec

	if err := j.persistLocked(); err != nil {
		rec.Revoked = origRevoked
		j.records[operationID] = rec
		return err
	}
	return nil
}

func (j *Journal) persistLocked() error {
	if j.path == "" {
		return nil
	}
	dir := filepath.Dir(j.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create journal directory %s: %w", dir, err)
	}

	list := make([]OperationRecord, 0, len(j.records))
	for _, rec := range j.records {
		list = append(list, rec)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal journal records: %w", err)
	}
	data = append(data, '\n')

	tmpPath := fmt.Sprintf("%s.tmp.%d.%d", j.path, os.Getpid(), time.Now().UnixNano())
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary journal file %s: %w", tmpPath, err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temporary journal file %s: %w", tmpPath, err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync temporary journal file %s: %w", tmpPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temporary journal file %s: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, j.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temporary journal file %s to %s: %w", tmpPath, j.path, err)
	}

	// Best-effort directory sync on platforms that support directory fsync
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}

	return nil
}
