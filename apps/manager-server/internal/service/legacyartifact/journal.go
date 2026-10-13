package legacyartifact

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrOperationReplayed = errors.New("operation already recorded in private journal")
	ErrOperationNotFound = errors.New("operation not found in private journal")
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

// OpenJournal opens or creates a JSON-backed Journal at the given path.
func OpenJournal(path string) (*Journal, error) {
	j := &Journal{
		path:    path,
		records: make(map[string]OperationRecord),
	}
	if path == "" {
		return j, nil
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var list []OperationRecord
		if err := json.Unmarshal(data, &list); err == nil {
			for _, rec := range list {
				j.records[rec.OperationID] = rec
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return j, nil
}

// RecordOperation records a new completed operation. If the operationID is already present,
// it returns ErrOperationReplayed.
func (j *Journal) RecordOperation(rec OperationRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if _, exists := j.records[rec.OperationID]; exists {
		return ErrOperationReplayed
	}
	j.records[rec.OperationID] = rec
	return j.persistLocked()
}

// GetOperation returns the record for an operationID if present.
func (j *Journal) GetOperation(operationID string) (OperationRecord, bool) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	rec, ok := j.records[operationID]
	return rec, ok
}

// RevokeOperation marks an operation as revoked.
func (j *Journal) RevokeOperation(operationID string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	rec, ok := j.records[operationID]
	if !ok {
		return ErrOperationNotFound
	}
	rec.Revoked = true
	j.records[operationID] = rec
	return j.persistLocked()
}

func (j *Journal) persistLocked() error {
	if j.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
		return err
	}
	list := make([]OperationRecord, 0, len(j.records))
	for _, rec := range j.records {
		list = append(list, rec)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(j.path, data, 0o600)
}
