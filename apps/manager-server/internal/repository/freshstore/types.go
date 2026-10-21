// Package freshstore persists the bounded Fresh product checkpoint and its
// authentication hashes in the actual Manager SQLite settings store.
package freshstore

import (
	"context"
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
	Operation bootstrap.FreshOperation
	Authority Authority
	Admin     *model.AdminCredential
	// Canonical server-owned connection. ManagementKey must be a Protector
	// ciphertext at this repository boundary, never a plaintext journal field.
	Connection *model.ManagerCPAConnectionConfig
	// Same Fresh CAS authority; encrypted resource snapshot is never public.
	AdoptionCiphertext string
	Retry              *RetryJournal
	// Corrupt Config metadata locks only Config. Other Fresh domains must not
	// erase it or lose their existing Administrator/read-only access.
	RetryInvalid bool
}

// RetryJournal uses the existing Fresh CAS/store. It contains metadata only;
// Runtime alone owns full-file recovery material and source reservations.
type RetryJournal struct {
	Latest     string                    `json:"latest"`
	Operations map[string]RetryOperation `json:"operations"`
}
type RetryOperation struct {
	ParentID      string `json:"parentId,omitempty"`
	ClaimRevision uint64 `json:"claimRevision,omitempty"`
	IntentDigest  string `json:"intentDigest,omitempty"`
	Attempted     bool   `json:"attempted,omitempty"`
	BeginRevision uint64 `json:"beginRevision,omitempty"`
	HTTP200       bool   `json:"http200,omitempty"`
	Receipt       string `json:"receipt,omitempty"`
	RequestID     string `json:"requestId"`
	Value         int    `json:"value"`
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
