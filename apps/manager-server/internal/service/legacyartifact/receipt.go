package legacyartifact

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Version 2 authenticates PID as part of the lifecycle evidence. Version 1
// receipts did not bind PID and must not be accepted under this contract.
const ReceiptVersion = 2

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

func (r *ExecutionReceipt) validateLifecycle() error {
	if err := r.ProcessEvidence.Validate(); err != nil {
		return err
	}
	if r.IssuedAtMS < r.ProcessEvidence.FinishedAtMS {
		return ErrProcessExecutionFailed
	}
	return nil
}

func (r *ExecutionReceipt) canonicalDigest() (string, error) {
	payload, err := r.CanonicalBytes()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// Validate checks lifecycle consistency; a trusted, qualified runner is still
// required to establish that these observations came from an actual process.
func (p ProcessEvidence) Validate() error {
	if p.PID <= 0 || p.ExitCode != 0 || p.StartedAtMS <= 0 ||
		p.FinishedAtMS < p.StartedAtMS || p.DurationMS <= 0 {
		return ErrProcessExecutionFailed
	}
	// Unix millisecond timestamps and an elapsed duration can differ by one
	// millisecond due to rounding. Larger disagreement is inconsistent evidence.
	elapsed := p.FinishedAtMS - p.StartedAtMS
	difference := elapsed - p.DurationMS
	if difference < -1 || difference > 1 {
		return ErrProcessExecutionFailed
	}
	return nil
}

type CheckpointEvidence struct {
	CacheAccountingV2        string `json:"cacheAccountingV2"`
	QuotaSnapshotLifecycleV1 string `json:"quotaSnapshotLifecycleV1"`
	MaintenanceLock          string `json:"maintenanceLock"`
}

// ExecutionReceipt is a foundation data format for input/output bundles,
// process observations and checkpoints. A signature does not establish Manager
// ownership or real execution; public trusted validation remains unavailable.
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
	// Delimiters cannot appear inside values: otherwise distinct receipts could
	// have identical canonical bytes and defeat complete journal binding.
	for _, value := range []string{r.OperationID, r.Release, r.SourceCommit, r.Asset,
		r.ExecutableSHA256, r.InputManifestDigest, r.OutputManifestDigest,
		r.InputFamily, r.OutputFamily, r.CheckpointEvidence.CacheAccountingV2,
		r.CheckpointEvidence.QuotaSnapshotLifecycleV1, r.CheckpointEvidence.MaintenanceLock} {
		if strings.Contains(value, "|") {
			return nil, ErrInvalidReceipt
		}
	}
	s := fmt.Sprintf("v%d|op:%s|rel:%s|sc:%s|asset:%s|exe:%s|in:%s|out:%s|inf:%s|outf:%s|pid:%d|exit:%d|start:%d|fin:%d|dur:%d|cac:%s|qsl:%s|mlock:%s|ts:%d",
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
		r.ProcessEvidence.PID,
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

// sign signs this receipt with the given authority and attaches the hex signature.
func (r *ExecutionReceipt) sign(auth *authority) error {
	payload, err := r.CanonicalBytes()
	if err != nil {
		return err
	}
	sig, err := auth.sign(payload)
	if err != nil {
		return err
	}
	r.Signature = sig
	return nil
}

// verifyAuthenticity checks the signature against the canonical payload.
func (r *ExecutionReceipt) verifyAuthenticity(auth *authority) error {
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
