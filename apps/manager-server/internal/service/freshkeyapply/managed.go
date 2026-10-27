package freshkeyapply

import (
	"context"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
	runtimeservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/runtime"
)

// Profiles of one installation authority. A Manager holds exactly one of them,
// selected once when the authority is constructed: the profile is never read
// from a request or from the environment.
const (
	// ProfilePrivate marks the Host-attested S1/S2 loopback profile. Its
	// authority is the Host-issued permit file.
	ProfilePrivate = "private"
	// ProfileManaged marks the authority derived from a committed Fresh
	// NewManaged installation instead of a Host-issued S1 permit file.
	ProfileManaged = "managed"
)

// managedGrantTTL bounds one derived step grant. It is never longer than the
// remaining install window, and it is re-derived for every read.
const managedGrantTTL = 60 * time.Second

// ManagedInstall is the durable authority of a default Fresh New Managed
// installation. It is read from the committed checkpoint and the protected
// Manager connection; no secret is cached by the service.
type ManagedInstall struct {
	RunID         string
	Generation    string
	Identity      string
	ArtifactID    string
	BaseURL       string
	ManagementKey string
}

// ManagedSource reloads and validates that authority on every call. It must
// fail closed with ErrUnavailable when the checkpoint, connection or secret is
// unavailable, unreadable, or bound to a different destination.
type ManagedSource func(context.Context) (ManagedInstall, error)

// NewManaged constructs the install-derived authority used by the default
// Compose installation. Runtime identity, generation and artifact are server
// observations of the same installation; the private loopback profile keeps
// its Host permit and is constructed with New instead.
func NewManaged(r *repo.Repository, keys *freshkeys.Service, runtime runtimeservice.RuntimeClient, endpoint string, source ManagedSource) *Service {
	s := New(r, keys, runtime)
	s.profile = ProfileManaged
	s.endpoint = endpoint
	s.source = source
	s.permit = func() (Permit, error) {
		if source == nil {
			return Permit{}, ErrUnavailable
		}
		now := time.Now()
		install, err := source(context.Background())
		if err != nil {
			return Permit{}, err
		}
		if install.RunID == "" || install.Generation == "" || install.Identity == "" || install.ArtifactID == "" || len(install.ManagementKey) != 64 {
			return Permit{}, ErrUnavailable
		}
		// The credential and the transport must address the same CPA; a moved
		// destination is a different installation, never a silent rebind.
		if cpa.NormalizeBaseURL(install.BaseURL) != cpa.NormalizeBaseURL(s.endpoint) {
			return Permit{}, ErrUnavailable
		}
		return Permit{
			RunID: install.RunID, Generation: install.Generation, ManagementKey: install.ManagementKey,
			IssuedAtMS: now.Add(-time.Second).UnixMilli(), ExpiresAtMS: now.Add(managedGrantTTL).UnixMilli(),
			Profile: ProfileManaged, Identity: install.Identity, ArtifactID: install.ArtifactID,
		}, nil
	}
	s.keyOperations = true
	s.wire = cpa.NewTransportForEndpoint(endpoint)
	s.keyPermit = s.managedKeyGrant
	return s
}

// ManagedEndpoint is the normalized destination CPA address this authority
// writes through. It is configuration, not a credential.
func (s *Service) ManagedEndpoint() string {
	if s == nil || s.profile != ProfileManaged {
		return ""
	}
	return s.endpoint
}

// Targets is the single installation-profile discriminator of the canonical
// HTTP surface: key install, key operation, chat admission and the gateway
// configuration page all ask this authority, so no caller can accept a
// checkpoint another one refuses. The profile is read from durable checkpoint
// facts, never from an environment variable or a request.
func (s *Service) Targets(op bootstrap.FreshOperation) bool {
	if s == nil || op.Strategy.Stage != "committed" {
		return false
	}
	switch s.profile {
	case ProfileManaged:
		return op.Strategy.Strategy == bootstrap.NewManaged && strings.HasPrefix(op.Strategy.DestinationConnectionIdentity, "managed:sha256:")
	case ProfilePrivate:
		// The private H1/direct profile names one attested runtime and artifact.
		// Its scope predates the managed checkpoint and does not require the
		// NewManaged strategy: the Host permit remains the write authority.
		return op.Strategy.TargetIdentity == cpa.PrivateKeyRuntime && op.Strategy.TargetArtifactID == cpa.PrivateKeyArtifact
	}
	return false
}

// managedKeyGrant derives the per-step authority for one durable key operation.
// The Host permit is replaced by durable facts only: the committed installation
// connection (management credential, run and generation) and the destination
// source digest observed immediately before the step.
func (s *Service) managedKeyGrant(ctx context.Context, operationID, step string) (KeyPermit, error) {
	if operationID == "" || (step != "add" && step != "revoke") {
		return KeyPermit{}, ErrUnavailable
	}
	p, err := s.permit()
	if err != nil {
		return KeyPermit{}, err
	}
	w, ok := s.wire.(keyWire)
	if !ok {
		return KeyPermit{}, ErrUnavailable
	}
	snap, err := w.KeySnapshot(ctx, p.ManagementKey)
	if err != nil || len(snap.SourceSHA256) != 64 {
		return KeyPermit{}, ErrUnavailable
	}
	now := time.Now()
	expires := now.Add(managedGrantTTL).UnixMilli()
	if expires > p.ExpiresAtMS {
		expires = p.ExpiresAtMS
	}
	if expires <= now.UnixMilli() {
		return KeyPermit{}, ErrUnavailable
	}
	return KeyPermit{
		Version: 1, SourceID: p.RunID, Revision: 1, OperationID: operationID, Step: step,
		RunID: p.RunID, Generation: p.Generation, SourceSHA256: snap.SourceSHA256,
		IssuedAtMS: now.UnixMilli(), ExpiresAtMS: expires,
	}, nil
}

// targetProof is the caller-observed target handed to the single write. A
// default install replaces the private attestation flags with the committed
// checkpoint and the authenticated destination read-back.
func (s *Service) targetProof(target model.RuntimeObservedStatus, p Permit) cpa.TargetProof {
	proof := cpa.TargetProof{Identity: string(target.Identity), Generation: uint64(target.Generation), Ready: true, ExpiresAtMS: p.ExpiresAtMS, ProviderSeed: p.S2B}
	if s.profile == ProfileManaged {
		if target.ActiveGatewayArtifact != nil {
			proof.ArtifactID = string(target.ActiveGatewayArtifact.ArtifactID)
		}
		return proof
	}
	proof.ArtifactID = cpa.PrivateKeyArtifact
	proof.SourceExclusive, proof.Disposable = true, true
	return proof
}
