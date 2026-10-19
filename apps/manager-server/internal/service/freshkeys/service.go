// Package freshkeys implements Manager-only staging. No method writes CPA.
package freshkeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/identity"
	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

var ErrRequest = errors.New("invalid creation request")
var ErrNotFound = errors.New("creation result not found")

type Key struct {
	ID            identity.APIKeyID `json:"id"`
	RequestID     string            `json:"requestId"`
	MaskedPreview string            `json:"maskedPreview"`
	CreatedAtMS   int64             `json:"createdAtMs"`
	ManagerState  string            `json:"managerState"`
	RuntimeState  string            `json:"runtimeState"`
}
type Result struct {
	Key           Key    `json:"key"`
	CreatedSecret string `json:"createdSecret,omitempty"`
}
type Service struct {
	repo      *repo.Repository
	protector *security.Protector
}

func New(r *repo.Repository, p *security.Protector) *Service { return &Service{r, p} }
func ValidRequestID(id string) bool                          { _, err := identity.ParseAPIKeyID(id); return err == nil }
func public(v repo.Record) Key {
	return Key{v.Identity.ID, v.RequestID, v.Preview, v.Identity.CreatedAtMS, "persisted", "not_applied"}
}
func digest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
func (s *Service) List(ctx context.Context) ([]Key, error) {
	if s.protector == nil {
		return nil, errors.New("secret protection unavailable")
	}
	records, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]Key, 0, len(records))
	for _, v := range records {
		secret, err := s.protector.UnprotectString(v.Ciphertext)
		if err != nil || digest(secret) != v.Hash || len(secret) != 73 || v.Preview != "cpamp_sk_…"+secret[len(secret)-4:] {
			return nil, errors.New("staged client secret unavailable")
		}
		keys = append(keys, public(v))
	}
	return keys, nil
}
func (s *Service) Lookup(ctx context.Context, requestID string) (Key, error) {
	if !ValidRequestID(requestID) {
		return Key{}, ErrRequest
	}
	keys, err := s.List(ctx)
	if err != nil {
		return Key{}, err
	}
	for _, key := range keys {
		if key.RequestID == requestID {
			return key, nil
		}
	}
	return Key{}, ErrNotFound
}
func (s *Service) Create(ctx context.Context, requestID string) (Result, bool, error) {
	key, err := s.Lookup(ctx, requestID)
	if err == nil {
		return Result{Key: key}, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Result{}, false, err
	}
	id, err := identity.NewAPIKeyID()
	if err != nil {
		return Result{}, false, err
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return Result{}, false, err
	}
	secret := "cpamp_sk_" + hex.EncodeToString(raw)
	ciphertext, err := s.protector.ProtectString(secret)
	if err != nil {
		return Result{}, false, err
	}
	now := time.Now().UnixMilli()
	v := repo.Record{RequestID: requestID, Identity: identity.APIKeyIdentity{ID: id, Revision: identity.InitialRevision, Lifecycle: identity.LifecycleActive, CreatedAtMS: now, UpdatedAtMS: now}, Ciphertext: ciphertext, Hash: digest(secret), Preview: "cpamp_sk_…" + secret[len(secret)-4:]}
	created, err := s.repo.Create(ctx, v)
	if err != nil {
		return Result{}, false, err
	}
	if !created {
		key, err = s.Lookup(ctx, requestID)
		return Result{Key: key}, false, err
	}
	return Result{Key: public(v), CreatedSecret: secret}, true, nil
}

// MatchStagedSecret verifies a Manager-staged credential against the existing
// canonical records. This result is NOT Runtime-effective Client authentication:
// callers must never use it alone to forward a model request to CPA.
func (s *Service) MatchStagedSecret(ctx context.Context, candidate string) (bool, error) {
	if s == nil || s.repo == nil || s.protector == nil {
		return false, errors.New("staged client key authority unavailable")
	}
	records, err := s.repo.List(ctx)
	if err != nil {
		return false, err
	}
	// The candidate shape matches secrets minted by Create. No caller input
	// is interpolated into SQL or emitted in a response/log.
	shape := len(candidate) == 73 && strings.HasPrefix(candidate, "cpamp_sk_")
	sum := sha256.Sum256([]byte(candidate))
	matched := 0
	for _, record := range records {
		secret, err := s.protector.UnprotectString(record.Ciphertext)
		if err != nil || len(secret) != 73 || digest(secret) != record.Hash ||
			record.Preview != "cpamp_sk_…"+secret[len(secret)-4:] {
			return false, errors.New("staged client key authority corrupt")
		}
		stored, err := hex.DecodeString(record.Hash)
		if err != nil || len(stored) != len(sum) {
			return false, errors.New("staged client key digest invalid")
		}
		if record.Identity.Lifecycle == identity.LifecycleActive {
			matched |= subtle.ConstantTimeCompare(sum[:], stored)
		}
	}
	return shape && matched == 1, nil
}
