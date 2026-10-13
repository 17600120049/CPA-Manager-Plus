package legacyartifact

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

const ReceiptVersion = 1

var (
	ErrInvalidReceipt = errors.New("invalid execution receipt")
	ErrForgedReceipt  = errors.New("forged or tampered execution receipt signature")
)

type ProcessEvidence struct {
	PID          int   `json:"pid"`
	ExitCode     int   `json:"exitCode"`
	StartedAtMS  int64 `json:"startedAtMs"`
	FinishedAtMS int64 `json:"finishedAtMs"`
	DurationMS   int64 `json:"durationMs"`
}

type CheckpointEvidence struct {
	CacheAccountingV2        string `json:"cacheAccountingV2"`
	QuotaSnapshotLifecycleV1 string `json:"quotaSnapshotLifecycleV1"`
	MaintenanceLock          string `json:"maintenanceLock"`
}

// ExecutionReceipt binds an authenticated v1.14.4 execution operation to its
// input and output bundles, process lifecycle, and verified migration checkpoints.
// It is signed by a Manager-owned private authority to prevent forgery or replay.
type ExecutionReceipt struct {
	Version              int                `json:"version"`
	OperationID          string             `json:"operationId"`
	Release              string             `json:"release"`
	SourceCommit         string             `json:"sourceCommit"`
	Asset                string             `json:"asset"`
	ExecutableSHA256     string             `json:"executableSHA256"`
	InputManifestDigest  string             `json:"inputManifestDigest"`
	OutputManifestDigest string             `json:"outputManifestDigest"`
	InputFamily          string             `json:"inputFamily"`
	OutputFamily         string             `json:"outputFamily"`
	ProcessEvidence      ProcessEvidence    `json:"processEvidence"`
	CheckpointEvidence   CheckpointEvidence `json:"checkpointEvidence"`
	IssuedAtMS           int64              `json:"issuedAtMs"`
	Signature            string             `json:"signature"`
}

// GenerateOperationID generates a 16-byte cryptographically secure hex ID.
func GenerateOperationID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CanonicalBytes formats the invariant fields in a deterministic, strict order.
// The signature field is excluded so that it authenticates exactly this canonical payload.
func (r *ExecutionReceipt) CanonicalBytes() ([]byte, error) {
	if r == nil || r.Version != ReceiptVersion || r.OperationID == "" || r.Release == "" ||
		r.SourceCommit == "" || r.Asset == "" || r.ExecutableSHA256 == "" ||
		r.InputManifestDigest == "" || r.OutputManifestDigest == "" {
		return nil, ErrInvalidReceipt
	}
	s := fmt.Sprintf("v%d|op:%s|rel:%s|sc:%s|asset:%s|exe:%s|in:%s|out:%s|inf:%s|outf:%s|exit:%d|start:%d|fin:%d|dur:%d|cac:%s|qsl:%s|mlock:%s|ts:%d",
		r.Version,
		r.OperationID,
		r.Release,
		r.SourceCommit,
		r.Asset,
		r.ExecutableSHA256,
		r.InputManifestDigest,
		r.OutputManifestDigest,
		r.InputFamily,
		r.OutputFamily,
		r.ProcessEvidence.ExitCode,
		r.ProcessEvidence.StartedAtMS,
		r.ProcessEvidence.FinishedAtMS,
		r.ProcessEvidence.DurationMS,
		r.CheckpointEvidence.CacheAccountingV2,
		r.CheckpointEvidence.QuotaSnapshotLifecycleV1,
		r.CheckpointEvidence.MaintenanceLock,
		r.IssuedAtMS,
	)
	return []byte(s), nil
}

// Sign signs this receipt with the given Authority and attaches the hex signature.
func (r *ExecutionReceipt) Sign(auth *Authority) error {
	payload, err := r.CanonicalBytes()
	if err != nil {
		return err
	}
	sig, err := auth.Sign(payload)
	if err != nil {
		return err
	}
	r.Signature = sig
	return nil
}

// VerifyAuthenticity checks the signature against the canonical payload.
func (r *ExecutionReceipt) VerifyAuthenticity(auth *Authority) error {
	if r == nil || r.Signature == "" {
		return ErrInvalidReceipt
	}
	payload, err := r.CanonicalBytes()
	if err != nil {
		return err
	}
	if !auth.Verify(payload, r.Signature) {
		return ErrForgedReceipt
	}
	return nil
}
