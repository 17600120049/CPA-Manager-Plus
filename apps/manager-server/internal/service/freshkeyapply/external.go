package freshkeyapply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/identity"
	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
)

// ProfileExternal marks the authority of a committed Fresh Connect External
// installation: the user's own CPA, reached through the protected Manager
// connection with the management credential the user supplied. It is neither the
// managed destination nor the Host-issued private permit, and this Manager owns
// no Runtime for it, so no lifecycle, artifact or readiness fact exists here.
const ProfileExternal = "external"

// ErrExternalUnavailable reports an External authority that cannot be resolved:
// no committed checkpoint, no protected connection, or a read-back the user's CPA
// did not answer. It is separate from ErrUnavailable because that one names the
// private profile's Host proof, which an External installation never holds.
var ErrExternalUnavailable = errors.New("external cpa authority unavailable")

// externalIdentityPrefix names an External connection by the digest of its
// normalized address. The strategy service persists exactly this value, so the
// address digest is the only comparable durable fact about the connection: the
// management credential itself is a protected envelope.
const externalIdentityPrefix = "external:sha256:"

// ExternalIdentity is the connection identity of one External CPA address.
func ExternalIdentity(baseURL string) string {
	digest := sha256.Sum256([]byte(baseURL))
	return externalIdentityPrefix + hex.EncodeToString(digest[:])
}

// ExternalGeneration derives the proof generation of one External target from the
// committed connection identity and the digest of the configuration read back
// from the user's CPA. Neither half is request input, so a caller cannot choose
// the generation it is authorized against.
func ExternalGeneration(connectionIdentity, configSHA256 string) string {
	digest := sha256.Sum256([]byte(connectionIdentity + "\n" + configSHA256))
	return externalIdentityPrefix + hex.EncodeToString(digest[:])
}

func validExternalIdentity(value string) bool {
	return len(value) == len(externalIdentityPrefix)+64 &&
		value[:len(externalIdentityPrefix)] == externalIdentityPrefix &&
		identity.IsValidSHA256Hex(value[len(externalIdentityPrefix):])
}

// ExternalInstall is the durable authority of a committed External installation.
// It is re-derived from the checkpoint, the protected Manager connection and a
// read-back of the user's CPA on every call; no credential is cached.
type ExternalInstall struct {
	// Identity is external:sha256:<digest of the normalized committed address>.
	Identity string
	// ConfigSHA256 is the digest of the configuration read back from the user's
	// CPA when this authority was resolved.
	ConfigSHA256 string
	// Checkpoint is the committed strategy generation the page and the key
	// journal pass back, exactly as the managed destination's is.
	Checkpoint uint64
	// BaseURL is the normalized address the Manager connection holds.
	BaseURL string
	// ManagementKey is the decrypted credential of that connection.
	ManagementKey string
}

// Generation binds the connection identity to the read-back configuration. A
// configuration the user changed afterwards is a different generation, so a step
// granted against the previous one never authorizes a write against the new one.
func (i ExternalInstall) Generation() string {
	return ExternalGeneration(i.Identity, i.ConfigSHA256)
}

// ExternalSource reloads and validates the External authority on every call. It
// MUST fail closed with ErrExternalUnavailable when the checkpoint, the protected
// connection or the read-back is unavailable, and it MUST return
// cpa.ErrManagementRejected when the user's CPA refuses the credential.
type ExternalSource func(context.Context) (ExternalInstall, error)

// externalWire is the External CPA v8 surface this authority drives. There is no
// managed template and no provider seed: the only comparable facts are key
// membership and the whole-document digest of the user's own configuration.
type externalWire interface {
	ExternalKeySnapshot(context.Context, string) (cpa.ExternalKeySnapshot, error)
	ExternalWriteKeySet(context.Context, string, []string) error
	ExternalSampleKeySet(context.Context, []string, []string) (bool, error)
}

// NewExternal constructs the authority of a committed Connect External
// installation. The key surfaces and the gateway configuration page act through
// it; chat admission, gateway admission and the Runtime surfaces never do.
func NewExternal(r *repo.Repository, keys *freshkeys.Service, source ExternalSource) *Service {
	s := New(r, keys, nil)
	s.profile = ProfileExternal
	s.externalSource = source
	s.externalWire = func(endpoint string) externalWire { return cpa.NewTransportForEndpoint(endpoint) }
	// The adapter is the wire every step of this profile speaks: it resolves the
	// committed target first, so no step is taken against a stale credential.
	s.wire = externalKeyAuthority{s: s}
	s.keyOperations = true
	s.permit = s.externalPermit
	s.keyPermit = s.externalKeyGrant
	return s
}

// externalPermit is the step authority of the currently resolved External
// target. Its generation is the digest-derived one, so a step is bound to the
// configuration the user's CPA presented when it was granted.
func (s *Service) externalPermit() (Permit, error) {
	install, err := s.externalAuthority(context.Background())
	if err != nil {
		return Permit{}, err
	}
	now := time.Now()
	return Permit{
		RunID: install.Identity, Generation: install.Generation(), ManagementKey: install.ManagementKey,
		IssuedAtMS: now.UnixMilli(), ExpiresAtMS: now.Add(managedGrantTTL).UnixMilli(),
		Profile: ProfileExternal, Identity: install.Identity,
	}, nil
}

// externalAuthority resolves and validates the committed External target. The
// generation is recomputed from the identity and the read-back digest, so a
// source that returns anything else is refused rather than trusted.
func (s *Service) externalAuthority(ctx context.Context) (ExternalInstall, error) {
	if s == nil || s.profile != ProfileExternal || s.externalSource == nil {
		return ExternalInstall{}, ErrExternalUnavailable
	}
	install, err := s.externalSource(ctx)
	if err != nil {
		if errors.Is(err, cpa.ErrManagementRejected) {
			return ExternalInstall{}, cpa.ErrManagementRejected
		}
		if errors.Is(err, ErrGeneration) || errors.Is(err, cpa.ErrSourceConflict) || errors.Is(err, cpa.ErrApplyUnknown) {
			return ExternalInstall{}, err
		}
		return ExternalInstall{}, ErrExternalUnavailable
	}
	if install.Checkpoint == 0 || len(install.ManagementKey) != 64 || install.BaseURL == "" ||
		!validExternalIdentity(install.Identity) || !identity.IsValidSHA256Hex(install.ConfigSHA256) {
		return ExternalInstall{}, ErrExternalUnavailable
	}
	return install, nil
}

// ExternalTarget is the External authority's answer for exactly the committed
// generation the caller holds. It is the gateway configuration page's entry
// point: the address and the credential of the CPA this installation owns.
func (s *Service) ExternalTarget(ctx context.Context, generation uint64) (string, string, error) {
	install, err := s.externalAuthority(ctx)
	if err != nil {
		return "", "", err
	}
	if install.Checkpoint != generation {
		return "", "", ErrGeneration
	}
	return install.BaseURL, install.ManagementKey, nil
}

// externalTarget is the same answer for the key surfaces, which also need the
// identity and the read-back digest.
func (s *Service) externalTarget(ctx context.Context, generation uint64) (ExternalInstall, error) {
	install, err := s.externalAuthority(ctx)
	if err != nil {
		return ExternalInstall{}, err
	}
	if install.Checkpoint != generation {
		return ExternalInstall{}, ErrGeneration
	}
	return install, nil
}

// ExternalEndpoint is the normalized address of the committed External target. It
// is configuration, not a credential, and it is empty for every other profile.
func (s *Service) ExternalEndpoint(ctx context.Context) (string, error) {
	install, err := s.externalAuthority(ctx)
	if err != nil {
		return "", err
	}
	return install.BaseURL, nil
}

func (s *Service) externalKeyWire(install ExternalInstall) externalWire {
	if s.externalWire == nil {
		return nil
	}
	return s.externalWire(install.BaseURL)
}

// ExternalKeyPresence reports, per staged key, whether the user's CPA carries it.
// It is the read-back behind the key list: a Manager record alone never claims an
// effect the CPA does not show.
func (s *Service) ExternalKeyPresence(ctx context.Context, ids []identity.APIKeyID) (map[identity.APIKeyID]bool, error) {
	install, err := s.externalAuthority(ctx)
	if err != nil {
		return nil, err
	}
	w := s.externalKeyWire(install)
	if w == nil {
		return nil, ErrExternalUnavailable
	}
	snapshot, err := w.ExternalKeySnapshot(ctx, install.ManagementKey)
	if err != nil {
		return nil, err
	}
	present := make(map[identity.APIKeyID]bool, len(ids))
	for _, id := range ids {
		// The staged secret is decrypted here and never leaves this process.
		ok := false
		if err := s.keys.WithStagedSecret(ctx, string(id), func(secret string) error {
			ok = slices.Contains(snapshot.Keys, secret)
			return nil
		}); err != nil {
			return nil, err
		}
		present[id] = ok
	}
	return present, nil
}

// ExternalKeyPresenceOf is ExternalKeyPresence for one key, for the routes that
// answer about exactly one record.
func (s *Service) ExternalKeyPresenceOf(ctx context.Context, id identity.APIKeyID) (bool, error) {
	present, err := s.ExternalKeyPresence(ctx, []identity.APIKeyID{id})
	if err != nil {
		return false, err
	}
	return present[id], nil
}

// externalSecretPresent reports whether the staged secret is one of the client
// credentials the user's CPA currently carries.
func (s *Service) externalSecretPresent(ctx context.Context, rawID string, snapshot cpa.ExternalKeySnapshot) (bool, error) {
	if _, err := identity.ParseAPIKeyID(rawID); err != nil {
		return false, freshkeys.ErrRequest
	}
	present := false
	err := s.keys.WithStagedSecret(ctx, rawID, func(secret string) error {
		present = slices.Contains(snapshot.Keys, secret)
		return nil
	})
	return present, err
}

// externalApply makes one staged key a client credential of the user's CPA and
// proves it by reading the list back. A key the CPA already carries is not
// written again, so a replay never duplicates a credential; the write itself is
// sent exactly once, and a refused management credential is the typed blocker,
// never a retry reason.
func (s *Service) externalApply(ctx context.Context, id string, generation uint64) (State, error) {
	result := State{KeyID: id, RuntimeState: "effect_unknown"}
	if _, err := s.keys.Detail(ctx, id); err != nil {
		return result, err
	}
	result.ManagerStaged = true
	install, err := s.externalTarget(ctx, generation)
	if err != nil {
		return result, err
	}
	w := s.externalKeyWire(install)
	if w == nil {
		return result, ErrExternalUnavailable
	}
	err = s.keys.WithStagedSecret(ctx, id, func(secret string) error {
		snapshot, err := w.ExternalKeySnapshot(ctx, install.ManagementKey)
		if err != nil {
			return err
		}
		if slices.Contains(snapshot.Keys, secret) {
			return nil
		}
		result.Attempted = true
		if err = w.ExternalWriteKeySet(ctx, install.ManagementKey, append(slices.Clone(snapshot.Keys), secret)); err != nil {
			return err
		}
		back, err := w.ExternalKeySnapshot(ctx, install.ManagementKey)
		if err != nil {
			return err
		}
		if slices.Contains(back.Keys, secret) {
			return nil
		}
		return cpa.ErrApplyUnknown
	})
	if err != nil {
		return result, err
	}
	return s.externalObserve(ctx, id, generation)
}

// externalObserve reads the user's CPA and reports the presence of this key. It
// claims no window this Manager cannot renew, so its evidence expires with the
// observation itself.
func (s *Service) externalObserve(ctx context.Context, id string, generation uint64) (State, error) {
	result := State{KeyID: id, RuntimeState: "effect_unknown"}
	if _, err := s.keys.Detail(ctx, id); err != nil {
		return result, err
	}
	result.ManagerStaged = true
	present, err := s.ExternalKeyPresenceOf(ctx, identity.APIKeyID(id))
	if err != nil {
		return result, err
	}
	if _, err = s.externalTarget(ctx, generation); err != nil {
		return result, err
	}
	result.Attempted = present
	result.InstallAvailable = !present
	result.RuntimeGeneration = strconv.FormatUint(generation, 10)
	if !present {
		return result, nil
	}
	now := time.Now()
	result.CPAConfigured = true
	result.RuntimeState = "effective_observed"
	result.ObservedFromMS = now.UnixMilli()
	result.ObservedUntilMS = now.UnixMilli()
	result.ExpiresAtMS = now.Add(5 * time.Second).UnixMilli()
	return result, nil
}

// externalInstalledKey names the one credential this Manager staged that the
// user's CPA currently accepts. Revoke and replace act on exactly one key, so an
// ambiguous set is a conflict rather than a guess.
func (s *Service) externalInstalledKey(ctx context.Context) (string, error) {
	if o, err := s.repo.LatestOperation(ctx); err != nil {
		return "", err
	} else if o != nil {
		if o.Outcome != "committed" {
			return "", ErrExternalUnavailable
		}
		return o.KeeperID, nil
	}
	ids, err := s.keys.IdentityIDs(ctx)
	if err != nil {
		return "", err
	}
	present, err := s.ExternalKeyPresence(ctx, ids)
	if err != nil {
		return "", err
	}
	carried := make([]string, 0, 1)
	for _, id := range ids {
		if present[id] {
			carried = append(carried, string(id))
		}
	}
	if len(carried) != 1 {
		return "", repo.ErrConflict
	}
	return carried[0], nil
}

// externalKeyGrant derives the per-step authority for one durable External key
// operation from the live target: the connection identity, the checkpoint
// generation and the configuration digest observed immediately before the step.
func (s *Service) externalKeyGrant(ctx context.Context, operationID, step string) (KeyPermit, error) {
	if operationID == "" || (step != "add" && step != "revoke") {
		return KeyPermit{}, ErrExternalUnavailable
	}
	install, err := s.externalAuthority(ctx)
	if err != nil {
		return KeyPermit{}, err
	}
	w := s.externalKeyWire(install)
	if w == nil {
		return KeyPermit{}, ErrExternalUnavailable
	}
	snapshot, err := w.ExternalKeySnapshot(ctx, install.ManagementKey)
	if err != nil || !identity.IsValidSHA256Hex(snapshot.SourceSHA256) {
		if err != nil {
			return KeyPermit{}, err
		}
		return KeyPermit{}, ErrExternalUnavailable
	}
	now := time.Now()
	return KeyPermit{
		Version: 1, SourceID: install.Identity, Revision: 1, OperationID: operationID, Step: step,
		RunID: install.Identity, Generation: install.Generation(),
		SourceSHA256: snapshot.SourceSHA256,
		IssuedAtMS:   now.UnixMilli(), ExpiresAtMS: now.Add(managedGrantTTL).UnixMilli(),
	}, nil
}

// externalKeyAuthority adapts the External CPA to the key loop the durable
// journal already speaks. Every method resolves the committed target first and
// refuses a credential that is not the current one, so a step can never be taken
// against a connection this installation no longer proves.
type externalKeyAuthority struct{ s *Service }

func (a externalKeyAuthority) resolved(ctx context.Context, management string) (ExternalInstall, externalWire, error) {
	install, err := a.s.externalAuthority(ctx)
	if err != nil {
		return ExternalInstall{}, nil, err
	}
	if management != install.ManagementKey {
		return ExternalInstall{}, nil, ErrGeneration
	}
	w := a.s.externalKeyWire(install)
	if w == nil {
		return ExternalInstall{}, nil, ErrExternalUnavailable
	}
	return install, w, nil
}

func (a externalKeyAuthority) KeySnapshot(ctx context.Context, management string) (cpa.KeySnapshot, error) {
	install, w, err := a.resolved(ctx, management)
	if err != nil {
		return cpa.KeySnapshot{}, err
	}
	snapshot, err := w.ExternalKeySnapshot(ctx, install.ManagementKey)
	if err != nil {
		return cpa.KeySnapshot{}, err
	}
	return cpa.KeySnapshot{Keys: snapshot.Keys, SourceSHA256: snapshot.SourceSHA256, SourceID: install.Identity, Generation: install.Generation()}, nil
}

func (a externalKeyAuthority) WriteKeySet(ctx context.Context, management string, keys []string) error {
	install, w, err := a.resolved(ctx, management)
	if err != nil {
		return err
	}
	return w.ExternalWriteKeySet(ctx, install.ManagementKey, keys)
}

// SampleKeySet probes exactly the keys one step added or removed. An External CPA
// may have client authentication disabled and may carry credentials this Manager
// never staged, so only the listed keys are sampled.
func (a externalKeyAuthority) SampleKeySet(ctx context.Context, accepted, rejected []string) (bool, error) {
	install, err := a.s.externalAuthority(ctx)
	if err != nil {
		return false, err
	}
	w := a.s.externalKeyWire(install)
	if w == nil {
		return false, ErrExternalUnavailable
	}
	return w.ExternalSampleKeySet(ctx, accepted, rejected)
}

// The External profile owns no install, no chat admission and no Runtime
// observation. These three are unreachable for it — Apply and Observe are
// dispatched to the External read-back before any of them is called — and they
// refuse rather than pretend.
func (a externalKeyAuthority) InstallOne(context.Context, cpa.TargetProof, string, string) (cpa.InstallResult, error) {
	return cpa.InstallResult{}, ErrExternalUnavailable
}

func (a externalKeyAuthority) Configured(context.Context, string, string) (bool, error) {
	return false, ErrExternalUnavailable
}

func (a externalKeyAuthority) AuthSweep(context.Context, string) (bool, error) {
	return false, ErrExternalUnavailable
}
