package freshkeyapply

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
	runtimeservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/runtime"
)

var ErrUnavailable = errors.New("private S1 host proof unavailable")
var ErrGeneration = errors.New("private S1 generation mismatch")

type State struct {
	KeyID             string `json:"keyId"`
	ManagerStaged     bool   `json:"managerStaged"`
	CPAConfigured     bool   `json:"cpaConfigured"`
	RuntimeState      string `json:"runtimeState"`
	RuntimeGeneration string `json:"runtimeGeneration"`
	ObservedFromMS    int64  `json:"observedFromMs"`
	ObservedUntilMS   int64  `json:"observedUntilMs"`
	ExpiresAtMS       int64  `json:"expiresAtMs"`
	IngressAuthorized bool   `json:"ingressAuthorized"`
	InstallAvailable  bool   `json:"installAvailable"`
	Attempted         bool   `json:"attempted"`
}

type wire interface {
	InstallOne(context.Context, cpa.TargetProof, string, string) (cpa.InstallResult, error)
	Configured(context.Context, string, string) (bool, error)
	AuthSweep(context.Context, string) (bool, error)
}
type Service struct {
	h1            *cpa.H1Coordinator
	mu            sync.Mutex
	repo          *repo.Repository
	keys          *freshkeys.Service
	runtime       runtimeservice.RuntimeClient
	wire          wire
	permit        func() (Permit, error)
	keyPermit     func(context.Context, string, string) (KeyPermit, error)
	source        ManagedSource
	endpoint      string
	profile       string
	keyOperations bool
	startedAtMS   int64
}

func New(r *repo.Repository, keys *freshkeys.Service, runtime runtimeservice.RuntimeClient) *Service {
	s := &Service{repo: r, keys: keys, runtime: runtime, wire: cpa.NewTransport(), permit: loadPermit,
		keyPermit:     func(context.Context, string, string) (KeyPermit, error) { return loadKeyPermit() },
		profile:       ProfilePrivate,
		keyOperations: os.Getenv("CPAMP_PRIVATE_KEY_OPERATIONS") == "1", startedAtMS: time.Now().UnixMilli()}
	if os.Getenv("CPAMP_PRIVATE_S2B_CHAT") == "1" {
		s.wire = cpa.NewPrivateS2BTransport(func() (cpa.PrivateProviderSeed, error) { p, err := s.permit(); return p.S2B, err })
	}
	return s
}

// keyGrantHeld re-derives the grant for this operation step. A Host permit is a
// fixed file and must be identical; a managed grant is derived per call from
// the committed installation and the live destination source, so its validity
// window is re-established on every read instead of compared.
func (s *Service) keyGrantHeld(ctx context.Context, grant KeyPermit, operationID, step string) bool {
	current, err := s.keyPermit(ctx, operationID, step)
	if err != nil {
		return false
	}
	if s.profile == ProfileManaged {
		return current.OperationID == grant.OperationID && current.Step == grant.Step &&
			current.RunID == grant.RunID && current.Generation == grant.Generation &&
			current.SourceSHA256 == grant.SourceSHA256
	}
	return current == grant
}

func (s *Service) target(ctx context.Context, p Permit) (model.RuntimeObservedStatus, error) {
	if !p.valid(time.Now()) || s.runtime == nil {
		return model.RuntimeObservedStatus{}, ErrUnavailable
	}
	v, err := s.runtime.Status(ctx)
	if err != nil {
		return v, ErrUnavailable
	}
	if p.Profile == ProfileManaged {
		// The committed installation pins identity, generation and artifact.
		// A stale target is a generation conflict; a runtime that is not ready
		// or a different artifact is unavailable, exactly as for the private
		// profile.
		if string(v.Identity) != p.Identity || strconv.FormatUint(uint64(v.Generation), 10) != p.Generation {
			return v, ErrGeneration
		}
		if v.State != model.RuntimeStateReady || v.ActiveGatewayArtifact == nil || string(v.ActiveGatewayArtifact.ArtifactID) != p.ArtifactID {
			return v, ErrUnavailable
		}
		return v, nil
	}
	if string(v.Identity) != cpa.PrivateKeyRuntime || strconv.FormatUint(uint64(v.Generation), 10) != p.Generation {
		return v, ErrGeneration
	}
	if v.State != model.RuntimeStateReady || v.ActiveGatewayArtifact == nil || string(v.ActiveGatewayArtifact.ArtifactID) != cpa.PrivateKeyArtifact || v.ActiveGatewayArtifact.Version != "8.0.13" {
		return v, ErrUnavailable
	}
	return v, nil
}

// Apply consumes one durable attempt for the entire disposable installation,
// including cancelled/unknown writes. A new HTTP call/process cannot retry it.
func (s *Service) Apply(ctx context.Context, id string, generation uint64) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := State{KeyID: id, RuntimeState: "effect_unknown"}
	if op, err := s.repo.LatestOperation(ctx); err != nil || op != nil {
		return result, repo.ErrConflict
	}
	p, err := s.permit()
	if err != nil {
		return result, err
	}
	if p.Generation != strconv.FormatUint(generation, 10) {
		return result, ErrGeneration
	}
	target, err := s.target(ctx, p)
	if err != nil {
		return result, err
	}
	err = s.keys.WithStagedSecret(ctx, id, func(secret string) error {
		result.ManagerStaged = true
		if err := s.repo.ClaimInitialInstall(ctx, repo.InitialInstall{KeyID: id, RunID: p.RunID, Generation: p.Generation, AttemptedAtMS: time.Now().UnixMilli(), ProviderSeedSHA256: p.S2B.Binding()}); err != nil {
			return err
		}
		result.Attempted = true
		// Revalidate immediately before the one-shot transport, never retarget.
		if _, err := s.target(ctx, p); err != nil {
			return err
		}
		_, err := s.wire.InstallOne(ctx, s.targetProof(target, p), p.ManagementKey, secret)
		return err
	})
	if err != nil {
		return result, err
	}
	return s.observe(ctx, id, generation)
}

// Observe performs only reads and real CPA authentication samples. Its response
// describes a bounded window, expires in five seconds and is never persisted as
// effective. Every read rechecks the host lease and authenticated generation.
func (s *Service) Observe(ctx context.Context, id string, generation uint64) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observe(ctx, id, generation)
}
func (s *Service) observe(ctx context.Context, id string, generation uint64) (State, error) {
	result := State{KeyID: id, RuntimeState: "effect_unknown"}
	if _, err := s.keys.Detail(ctx, id); err != nil {
		return result, err
	}
	result.ManagerStaged = true
	attempt, err := s.repo.InitialInstall(ctx)
	if err != nil {
		return result, err
	}
	// This is evidence for this canonical Key only. Another Key consuming
	// the single S1 journal must still block future installs, not resolve this
	// Key's UNKNOWN response.
	result.Attempted = attempt != nil && attempt.KeyID == id
	p, err := s.permit()
	if err != nil {
		return result, nil
	}
	if p.Generation != strconv.FormatUint(generation, 10) {
		return result, ErrGeneration
	}
	if _, err = s.target(ctx, p); err != nil {
		return result, err
	}
	result.RuntimeGeneration = p.Generation
	result.InstallAvailable = attempt == nil
	if attempt == nil {
		return result, nil
	}
	installedID, err := s.installedKey(ctx)
	if err != nil {
		return result, err
	}
	if installedID != id {
		return result, nil
	}
	if attempt.Generation != p.Generation || attempt.RunID != p.RunID || attempt.ProviderSeedSHA256 != p.S2B.Binding() {
		return result, ErrGeneration
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	err = s.keys.WithStagedSecret(ctx, id, func(secret string) error {
		configured, err := s.wire.Configured(ctx, p.ManagementKey, secret)
		if err != nil {
			return err
		}
		result.CPAConfigured = configured
		if !configured {
			return nil
		}
		from := time.Now()
		for i := 0; i < 3; i++ {
			ok, err := s.wire.AuthSweep(ctx, secret)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
			if i < 2 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(250 * time.Millisecond):
				}
			}
		}
		if _, err := s.target(ctx, p); err != nil {
			return err
		}
		now := time.Now()
		if !p.valid(now) {
			return ErrUnavailable
		}
		result.RuntimeState = "effective_observed"
		result.ObservedFromMS = from.UnixMilli()
		result.ObservedUntilMS = now.UnixMilli()
		result.ExpiresAtMS = now.Add(5 * time.Second).UnixMilli()
		if result.ExpiresAtMS > p.ExpiresAtMS {
			result.ExpiresAtMS = p.ExpiresAtMS
		}
		return nil
	})
	if err == nil && s.h1 != nil && result.RuntimeState == "effective_observed" {
		t, ok := s.wire.(*cpa.Transport)
		if !ok {
			return result, ErrUnavailable
		}
		if err = s.h1.InitialConsumed(ctx, t, p.RunID, p.Generation, id); err != nil {
			return result, err
		}
	}
	return result, err
}
