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
	"unicode"
	"unicode/utf8"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/identity"
	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

var ErrRequest = errors.New("invalid creation request")
var ErrNotFound = errors.New("creation result not found")
var ErrConflict = repo.ErrConflict

type Key struct {
	ID            identity.APIKeyID  `json:"id"`
	RequestID     string             `json:"requestId"`
	MaskedPreview string             `json:"maskedPreview"`
	CreatedAtMS   int64              `json:"createdAtMs"`
	ManagerState  string             `json:"managerState"`
	RuntimeState  string             `json:"runtimeState"`
	Name          string             `json:"name"`
	NameRevision  int64              `json:"nameRevision"`
	Lifecycle     identity.Lifecycle `json:"lifecycle"`
}
type Result struct {
	Key           Key    `json:"key"`
	CreatedSecret string `json:"createdSecret,omitempty"`
}

// SourceRuntime resolves the runtime identity a new key's source binding
// belongs to. It returns "" while the installation has no committed target.
type SourceRuntime func(context.Context) (string, error)

// Option configures optional Fresh Key collaborators.
type Option func(*Service)

// WithSourceRuntime enables same-transaction source binding for created keys.
func WithSourceRuntime(source SourceRuntime) Option {
	return func(s *Service) { s.source = source }
}

type Service struct {
	repo      *repo.Repository
	protector *security.Protector
	source    SourceRuntime
}

func New(r *repo.Repository, p *security.Protector, options ...Option) *Service {
	s := &Service{repo: r, protector: p}
	for _, option := range options {
		option(s)
	}
	return s
}

func ValidRequestID(id string) bool { _, err := identity.ParseAPIKeyID(id); return err == nil }

// runtimeIdentity reports the committed installation target. A read failure
// only defers attribution to the startup backfill; it never fails key creation.
func (s *Service) runtimeIdentity(ctx context.Context) string {
	if s.source == nil {
		return ""
	}
	value, err := s.source(ctx)
	if err != nil {
		return ""
	}
	return value
}
func public(v repo.Record) Key {
	return Key{v.Identity.ID, v.RequestID, v.Preview, v.Identity.CreatedAtMS, "persisted", "not_applied", v.Name, v.NameRevision, v.Identity.Lifecycle}
}

// IdentityIDs lists the Canonical Keys of this workspace without touching any
// stored secret. Usage reads need identity only, so they never decrypt one.
func (s *Service) IdentityIDs(ctx context.Context) ([]identity.APIKeyID, error) {
	records, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]identity.APIKeyID, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.Identity.ID)
	}
	return ids, nil
}

func (s *Service) Detail(ctx context.Context, rawID string) (Key, error) {
	id, err := identity.ParseAPIKeyID(rawID)
	if err != nil {
		return Key{}, ErrRequest
	}
	keys, err := s.List(ctx)
	if err != nil {
		return Key{}, err
	}
	for _, key := range keys {
		if key.ID == id {
			return key, nil
		}
	}
	return Key{}, ErrNotFound
}

func (s *Service) Rename(ctx context.Context, rawID, name string, revision int64) (Key, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 120 || strings.ContainsFunc(name, unicode.IsControl) || revision < 1 {
		return Key{}, ErrRequest
	}
	key, err := s.Detail(ctx, rawID)
	if err != nil {
		return Key{}, err
	}
	if err := s.repo.Rename(ctx, key.ID, name, revision); err != nil {
		return Key{}, err
	}
	// Return the committed operation's metadata, not a later writer's readback.
	key.Name, key.NameRevision = name, revision+1
	return key, nil
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
	attempt, err := s.repo.InitialInstall(ctx)
	if err != nil {
		return nil, err
	}
	operation, err := s.repo.LatestOperation(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range records {
		secret, err := s.protector.UnprotectString(v.Ciphertext)
		if err != nil || digest(secret) != v.Hash || len(secret) != 73 || v.Preview != "cpamp_sk_…"+secret[len(secret)-4:] {
			return nil, errors.New("staged client secret unavailable")
		}
		key := public(v)
		if v.Identity.Lifecycle == identity.LifecycleSuperseded {
			key.RuntimeState = "effect_unknown"
		}
		if attempt != nil && attempt.KeyID == string(key.ID) {
			key.RuntimeState = "effect_unknown"
		}
		if operation != nil && (operation.KeeperID == string(key.ID) || operation.TargetID == string(key.ID)) {
			key.RuntimeState = "effect_unknown"
		}
		keys = append(keys, key)
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
	v := repo.Record{RequestID: requestID, Identity: identity.APIKeyIdentity{ID: id, Revision: identity.InitialRevision, Lifecycle: identity.LifecycleActive, CreatedAtMS: now, UpdatedAtMS: now}, Ciphertext: ciphertext, Hash: digest(secret), Preview: "cpamp_sk_…" + secret[len(secret)-4:], NameRevision: 1}
	created, err := s.repo.Create(ctx, v, s.runtimeIdentity(ctx))
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

// WithStagedSecret is an internal server-side consumer of the canonical encrypted
// record. No HTTP response may contain the secret passed to consume.
func (s *Service) WithStagedSecret(ctx context.Context, rawID string, consume func(string) error) error {
	return s.withSecret(ctx, rawID, true, consume)
}

// WithRetainedSecret is exclusively for read-only revocation reconciliation.
// Superseded identity history stays readable; it cannot grant authentication.
func (s *Service) WithRetainedSecret(ctx context.Context, rawID string, consume func(string) error) error {
	return s.withSecret(ctx, rawID, false, consume)
}
func (s *Service) withSecret(ctx context.Context, rawID string, active bool, consume func(string) error) error {
	key, err := s.Detail(ctx, rawID)
	if err != nil {
		return err
	}
	records, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Identity.ID != key.ID {
			continue
		}
		secret, err := s.protector.UnprotectString(record.Ciphertext)
		if err != nil || digest(secret) != record.Hash || active && record.Identity.Lifecycle != identity.LifecycleActive {
			return errors.New("staged client secret unavailable")
		}
		return consume(secret)
	}
	return ErrNotFound
}
