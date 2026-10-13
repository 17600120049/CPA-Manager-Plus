package legacyartifact

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrOperationReplayed  = errors.New("operation already recorded in private journal")
	ErrOperationNotFound  = errors.New("operation not found in private journal")
	ErrCorruptJournal     = errors.New("journal file is corrupt, malformed, or contains duplicate records")
	ErrInsecureJournal    = errors.New("journal file, lock, or private directory is insecure")
	ErrJournalUnavailable = errors.New("private journal is missing or unreadable")
)

type operationRecord struct {
	OperationID            string `json:"operationId"`
	ReceiptVersion         int    `json:"receiptVersion"`
	CanonicalReceiptSHA256 string `json:"canonicalReceiptSHA256"`
	Revoked                bool   `json:"revoked,omitempty"`
}

// Validate verifies that an operationRecord contains all critical authority fields.
func (r operationRecord) Validate() error {
	if strings.TrimSpace(r.OperationID) == "" {
		return errors.New("operationId is required in journal record")
	}
	if r.ReceiptVersion != ReceiptVersion {
		return errors.New("unsupported journal receipt version")
	}
	if digest, err := hex.DecodeString(r.CanonicalReceiptSHA256); err != nil || len(digest) != 32 {
		return errors.New("canonical receipt SHA256 is required in journal record")
	}
	return nil
}

// journal retains private receipt records for the foundation's replay and
// revocation checks. Persistent storage alone does not qualify Manager ownership
// or prove actual execution.
type journal struct {
	mu      sync.RWMutex
	path    string
	records map[string]operationRecord
}

func recordForReceipt(receipt *ExecutionReceipt) (operationRecord, error) {
	digest, err := receipt.canonicalDigest()
	if err != nil {
		return operationRecord{}, err
	}
	return operationRecord{OperationID: receipt.OperationID, ReceiptVersion: receipt.Version, CanonicalReceiptSHA256: digest}, nil
}

// openJournal opens an existing JSON-backed journal. Validation never creates
// a missing authority file or restores records from an in-memory cache.
// If the journal exists but is corrupt, malformed, contains duplicate records,
// or is an insecure symlink, it fails closed with an error.
func openJournal(path string) (*journal, error) {
	path = strings.TrimSpace(path)
	j := &journal{
		path:    path,
		records: make(map[string]operationRecord),
	}
	if path == "" {
		return nil, ErrJournalRequired
	}

	if err := j.loadFromDiskLocked(); err != nil {
		return nil, err
	}

	return j, nil
}

// openOrCreateJournal explicitly initializes a new private journal under the
// same cross-process lock used by writers. Runtime reloads never initialize it.
func openOrCreateJournal(path string) (*journal, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, ErrJournalRequired
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	j := &journal{path: path, records: make(map[string]operationRecord)}
	lock, err := j.acquireDiskLock()
	if err != nil {
		return nil, err
	}
	defer lock.Release()
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

func (j *journal) loadFromDiskLocked() (loadErr error) {
	defer func() {
		if loadErr != nil {
			j.records = make(map[string]operationRecord)
		}
	}()
	if j.path == "" {
		return nil
	}
	if err := checkPrivateDirectory(filepath.Dir(j.path)); err != nil {
		return errors.Join(ErrInsecureJournal, err)
	}
	if err := checkPrivateFile(j.path); err != nil {
		if errors.Is(err, errInsecurePrivateStorage) {
			return errors.Join(ErrInsecureJournal, err)
		}
		return errors.Join(ErrJournalUnavailable, err)
	}
	if err := checkPrivateLockPath(j.path + ".lock"); err != nil {
		return errors.Join(ErrInsecureJournal, err)
	}

	data, err := os.ReadFile(j.path)
	if err != nil {
		return errors.Join(ErrJournalUnavailable, fmt.Errorf("read journal file %s: %w", j.path, err))
	}

	var list []operationRecord
	if err := json.Unmarshal(data, &list); err != nil {
		return errors.Join(ErrCorruptJournal, fmt.Errorf("unmarshal journal %s: %w", j.path, err))
	}

	seenIDs := make(map[string]bool, len(list))
	newRecords := make(map[string]operationRecord, len(list))
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

func (j *journal) acquireDiskLock() (*fileLock, error) {
	lock, err := acquireFileLock(j.path + ".lock")
	if err != nil {
		j.records = make(map[string]operationRecord)
		if errors.Is(err, errInsecurePrivateStorage) {
			return nil, errors.Join(ErrInsecureJournal, err)
		}
		return nil, errors.Join(ErrJournalUnavailable, err)
	}
	return lock, nil
}

// Path returns the path of the journal file.
func (j *journal) Path() string {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.path
}

// recordOperation records a new completed operation. If the operationID is already present,
// it returns ErrOperationReplayed. Cross-process file locking ensures mutual exclusion.
// Persistence failures cause the entire record operation to fail and not be committed to memory.
func (j *journal) recordOperation(rec operationRecord) error {
	if err := rec.Validate(); err != nil {
		return err
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if j.path != "" {
		lock, err := j.acquireDiskLock()
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

// getOperation returns the record for an operationID if present.
func (j *journal) getOperation(operationID string) (operationRecord, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.path != "" {
		lock, err := j.acquireDiskLock()
		if err != nil {
			return operationRecord{}, false, errors.Join(ErrJournalUnavailable, err)
		}
		defer lock.Release()
		if err := j.loadFromDiskLocked(); err != nil {
			return operationRecord{}, false, err
		}
	}

	rec, ok := j.records[operationID]
	return rec, ok, nil
}

// revokeOperation marks an operation as revoked.
func (j *journal) revokeOperation(operationID string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.path != "" {
		lock, err := j.acquireDiskLock()
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

func (j *journal) persistLocked() error {
	if j.path == "" {
		return nil
	}
	dir := filepath.Dir(j.path)
	if err := checkPrivateDirectory(dir); err != nil {
		return errors.Join(ErrInsecureJournal, err)
	}

	list := make([]operationRecord, 0, len(j.records))
	for _, rec := range j.records {
		list = append(list, rec)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal journal records: %w", err)
	}
	data = append(data, '\n')

	tmpFile, err := os.CreateTemp(dir, ".legacy-journal-*")
	if err != nil {
		return fmt.Errorf("create temporary journal file: %w", err)
	}
	tmpPath := tmpFile.Name()

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
