// Package freshstore persists the bounded Fresh product checkpoint and its
// authentication hashes in the actual Manager SQLite settings store.
package freshstore

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

var (
	ErrNotFresh = errors.New("matching Fresh ownership unavailable")
	ErrConflict = errors.New("Fresh authority conflict")
)

// Authentication hashes are not part of the non-secret product checkpoint.
type Authority struct {
	ClaimHash          string `json:"claimHash,omitempty"`
	ClaimGeneration    uint64 `json:"claimGeneration"`
	ClaimExpiresAtMS   int64  `json:"claimExpiresAtMs"`
	ClaimExchanged     bool   `json:"claimExchanged"`
	ClaimClosed        bool   `json:"claimClosed"`
	SessionHash        string `json:"sessionHash,omitempty"`
	SessionExpiresAtMS int64  `json:"sessionExpiresAtMs"`
	SessionGeneration  uint64 `json:"sessionGeneration"`
	AdminCommitID      string `json:"adminCommitId,omitempty"`
}

type Record struct {
	Operation          bootstrap.FreshOperation
	Authority          Authority
	Admin              *model.AdminCredential
	Connection         *model.ManagerCPAConnectionConfig
	AdoptionCiphertext string
	// Config holds one journal per allowlisted config field, keyed by the
	// canonical CPA dotted path. ConfigInvalid records exactly which field has
	// damaged bytes: a corrupted field locks that field only, never the record.
	// ConfigDamaged keeps those exact bytes so a later write of another field
	// rewrites them unchanged instead of discarding an unreadable original.
	Config        map[string]*FieldJournal
	ConfigInvalid map[string]bool
	ConfigDamaged map[string]json.RawMessage
}

// FieldJournal is the Manager-side journal for ONE allowlisted config field. It
// uses the existing Fresh CAS/store and contains metadata only; Runtime alone
// owns full-file recovery material and source reservations.
type FieldJournal struct {
	Latest     string                     `json:"latest"`
	Operations map[string]ConfigOperation `json:"operations"`
}

// ConfigOperation is one single-field write attempt. RequestID is the Public API
// request ID and the journal key; Field is the canonical path it writes.
type ConfigOperation struct {
	ParentID      string `json:"parentId,omitempty"`
	ClaimRevision uint64 `json:"claimRevision,omitempty"`
	IntentDigest  string `json:"intentDigest,omitempty"`
	Attempted     bool   `json:"attempted,omitempty"`
	BeginRevision uint64 `json:"beginRevision,omitempty"`
	HTTP200       bool   `json:"http200,omitempty"`
	Receipt       string `json:"receipt,omitempty"`
	Field         string `json:"field,omitempty"`
	RequestID     string `json:"requestId"`
	Value         any    `json:"value"`
	SourceID      string `json:"sourceId"`
	Generation    string `json:"generation"`
	Revision      uint64 `json:"revision"`
	SHA256        string `json:"sha256"`
	State         string `json:"state"`
}

// Store exposes only Fresh CAS transactions, not arbitrary workflow execution.
// Create requires a pristine admitted store and inserts ownership first. Update
// revalidates identity/revision and canonical admin consistency under SQLite's
// immediate write transaction; it may insert admin once but never overwrite it.
type Store interface {
	Load(context.Context) (Record, error)
	Create(context.Context, bootstrap.FreshIdentity, *model.AdminCredential) (Record, error)
	Update(context.Context, bootstrap.FreshIdentity, uint64, func(*Record) error) (Record, error)
}
