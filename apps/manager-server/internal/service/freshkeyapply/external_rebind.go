package freshkeyapply

import (
	"context"
	"errors"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

// ErrRebindRefused reports a rebind this authority will not perform: an address
// that is not the committed one, a credential the user's CPA refused, a
// credential that does not serve the v8 contract, an installation that is not a
// committed Connect External target, or a request the caller built against
// another generation. It never carries a credential.
var ErrRebindRefused = errors.New("external rebind refused")

// ExternalRebindVerifier proves one candidate management credential against the
// user's CPA without persisting anything. It is the read that decides the rebind:
// an installation is only worth rebinding to a credential its own CPA answers.
type ExternalRebindVerifier func(ctx context.Context, baseURL string, managementKey string) (cpa.ExternalVersionReport, error)

// ObserveExternalRebindCredential is the production verifier: one authenticated
// management read that also classifies the version the CPA advertises.
func ObserveExternalRebindCredential(ctx context.Context, baseURL string, managementKey string) (cpa.ExternalVersionReport, error) {
	return cpa.ObserveExternalVersion(ctx, baseURL, managementKey)
}

// externalRebind is the durable record operation a rebind needs.
type externalRebindStore interface {
	freshstore.Store
	freshstore.ConnectionRebinder
}

// Rebind replaces the management credential of the committed External
// connection. The user's CPA is administered by the user, so a rotated management
// credential is a fact of their installation rather than an outage: only the
// administrator can supply the new one, and this is the one path that accepts it.
//
// The order is what makes it safe. The record revision is read before the
// candidate is proven, and it is the revision the write is conditional on: a save,
// a key write or another rebind that lands while the candidate is being verified
// therefore conflicts instead of being overwritten by a decision taken against a
// record this installation no longer holds. The candidate itself is proven with one
// authenticated read against exactly the committed address, so nothing is written
// for a credential that does not authenticate or that does not serve the v8
// contract this Manager writes. Only then is it encrypted and persisted, in the
// transaction that replaces the connection and bumps the record revision — which is
// what makes every write prepared against the previous credential a mismatch
// instead of a write against a credential this Manager no longer holds.
//
// The address is never changed here: a different address is a different
// installation, and reaching it is a reconnect rather than a rebind.
func (s *Service) Rebind(ctx context.Context, store externalRebindStore, admin *security.Protector, generation uint64,
	baseURL string, managementKey string) (freshstore.Record, error) {
	if s == nil || s.profile != ProfileExternal || store == nil || admin == nil {
		return freshstore.Record{}, ErrExternalUnavailable
	}
	if !cpa.ValidV8ManagementKey(managementKey) {
		// A credential CPA itself could never accept is refused before any read:
		// the shape rule is the same one every write enforces.
		return freshstore.Record{}, ErrRebindRefused
	}
	install, err := s.externalTarget(ctx, generation)
	if err != nil {
		return freshstore.Record{}, err
	}
	if strings.TrimSpace(baseURL) == "" {
		// The committed address is the only address a rebind may reach. A caller
		// that does not name it is asking for a reconnect, never a rebind.
		baseURL = install.BaseURL
	}
	if baseURL != install.BaseURL {
		return freshstore.Record{}, ErrRebindRefused
	}
	if managementKey == install.ManagementKey {
		// The candidate is the credential already stored. A rebind would move the
		// revision and claim a rotation that never happened, so it is refused
		// instead of reported as success.
		return freshstore.Record{}, ErrRebindRefused
	}
	// The revision is read here, before the verification that may take as long as
	// one CPA round trip: the write below is conditional on this exact revision, so
	// a record that moves during the verification conflicts instead of being
	// overwritten by a rebind decided against stale state.
	record, err := store.Load(ctx)
	if err != nil {
		return freshstore.Record{}, ErrExternalUnavailable
	}
	if record.Operation.Strategy.Strategy != bootstrap.ConnectExternal {
		return freshstore.Record{}, ErrExternalUnavailable
	}
	verify := s.externalRebindVerifier
	if verify == nil {
		verify = ObserveExternalRebindCredential
	}
	report, err := verify(ctx, install.BaseURL, managementKey)
	switch {
	case errors.Is(err, cpa.ErrManagementClientForbidden):
		// CPA refused this client, not the credential: an address ban or a
		// disabled remote management. It is reported unchanged so the page can
		// say that the same key may work again once the window passes.
		return freshstore.Record{}, cpa.ErrManagementClientForbidden
	case errors.Is(err, cpa.ErrManagementRejected):
		// The user's CPA refused the candidate, so nothing was proven and nothing
		// may be persisted. It is reported as the same typed rejection the reads
		// carry, so the page keeps asking for a credential instead of a retry.
		return freshstore.Record{}, cpa.ErrManagementRejected
	case err != nil:
		return freshstore.Record{}, err
	case report.WriteBlocked:
		return freshstore.Record{}, cpa.ErrExternalVersionUnsupported
	}
	ciphertext, err := admin.ProtectString(managementKey)
	if err != nil {
		return freshstore.Record{}, ErrExternalUnavailable
	}
	next := model.ManagerCPAConnectionConfig{CPABaseURL: install.BaseURL, ManagementKey: ciphertext}
	rebound, err := store.RebindConnection(ctx, record.Operation.Identity, record.Operation.Revision, next)
	if err != nil {
		if errors.Is(err, freshstore.ErrConflict) {
			// A save, a key write or another rebind moved the record after the
			// revision above was read — during the verification included. The page
			// re-reads and asks again: guessing which of the two writes won would
			// persist a credential the user did not intend.
			return freshstore.Record{}, ErrGeneration
		}
		if errors.Is(err, freshstore.ErrRebindRefused) {
			return freshstore.Record{}, ErrRebindRefused
		}
		return freshstore.Record{}, ErrExternalUnavailable
	}
	return rebound, nil
}
