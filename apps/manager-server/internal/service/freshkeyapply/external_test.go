package freshkeyapply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"path/filepath"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/identity"
	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
)

// externalManagement is the management credential of the fake user CPA. It is
// opaque to this Manager, so the tests use a shape only the length check knows.
const externalManagement = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeExternalCPA is a user-owned CPA: it carries a configuration, an arbitrary
// client-key list and one management credential, and it answers exactly the
// three v8 routes this Manager drives.
type fakeExternalCPA struct {
	config      string
	keys        []string
	management  string
	rejectOnce  bool
	rejectEvery bool
	writes      int
	server      *httptest.Server
}

func newFakeExternalCPA(t *testing.T, keys ...string) *fakeExternalCPA {
	t.Helper()
	f := &fakeExternalCPA{
		config:     `{"config-version":8,"access":{"api-keys":[]}}`,
		keys:       keys,
		management: externalManagement,
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.rejectEvery || (r.Header.Get("Authorization") != "Bearer "+f.management) {
			// A rotated management credential refuses every route, read included:
			// that is what makes it a blocker rather than an unavailable proof.
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/v8/management/config/access/api-keys" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(f.keys)
		case r.URL.Path == "/v8/management/config.yaml" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(f.config))
		case r.URL.Path == "/v8/management/config/access/api-keys" && r.Method == http.MethodPut:
			f.writes++
			if f.rejectOnce {
				f.rejectOnce = false
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var next []string
			if json.NewDecoder(r.Body).Decode(&next) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.keys = next
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v1/models":
			key := r.Header.Get("Authorization")
			key = strings.TrimPrefix(key, "Bearer ")
			if slices.Contains(f.keys, key) {
				_, _ = w.Write([]byte(`{"data":[]}`))
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

// source is the External authority of this fake CPA: the configuration digest is
// read from the CPA itself, exactly as the production source does.
func (f *fakeExternalCPA) source() ExternalSource {
	return func(context.Context) (ExternalInstall, error) {
		snapshot, err := cpa.NewTransportForEndpoint(f.server.URL).ExternalKeySnapshot(context.Background(), f.management)
		if err != nil {
			return ExternalInstall{}, err
		}
		return ExternalInstall{
			Identity:      ExternalIdentity(f.server.URL),
			ConfigSHA256:  snapshot.SourceSHA256,
			Checkpoint:    1,
			BaseURL:       f.server.URL,
			ManagementKey: f.management,
		}, nil
	}
}

// externalHarness is one Manager workspace over one fake user CPA.
type externalHarness struct {
	cpa     *fakeExternalCPA
	keys    *freshkeys.Service
	service *Service
}

func newExternalHarness(t *testing.T, keys ...string) *externalHarness {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = sqlite.Migrate(db); err != nil {
		t.Fatal(err)
	}
	r := repo.New(db)
	protector, err := security.NewProtector([]byte("isolated-external-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	h := &externalHarness{cpa: newFakeExternalCPA(t, keys...), keys: freshkeys.New(r, protector)}
	h.service = NewExternal(r, h.keys, h.cpa.source())
	return h
}

// staged stages one Manager key and returns its canonical ID and secret.
func (h *externalHarness) staged(t *testing.T, requestID string) (string, string) {
	t.Helper()
	result, _, err := h.keys.Create(context.Background(), requestID)
	if err != nil {
		t.Fatal(err)
	}
	return string(result.Key.ID), result.CreatedSecret
}

func TestExternalGenerationBindsTheConnectionToTheReadBack(t *testing.T) {
	identity := ExternalIdentity("https://cpa.example")
	if !validExternalIdentity(identity) {
		t.Fatalf("identity = %q", identity)
	}
	digest := sha256.Sum256([]byte("https://cpa.example"))
	if identity != "external:sha256:"+hex.EncodeToString(digest[:]) {
		t.Fatalf("identity is not the address digest: %q", identity)
	}
	one := ExternalGeneration(identity, strings.Repeat("a", 64))
	if one != ExternalGeneration(identity, strings.Repeat("a", 64)) {
		t.Fatal("generation is not a function of its facts")
	}
	for _, other := range []string{
		ExternalGeneration(identity, strings.Repeat("b", 64)),
		ExternalGeneration(ExternalIdentity("https://other.example"), strings.Repeat("a", 64)),
	} {
		if other == one {
			t.Fatal("a changed connection or read-back kept the generation")
		}
	}
}

// The External authority never resolves a target of another profile, an
// incomplete installation, or a credential the tests did not supply.
func TestExternalAuthorityFailsClosed(t *testing.T) {
	harness := newExternalHarness(t)
	ctx := context.Background()
	for _, scenario := range []struct {
		name  string
		edit  func(ExternalInstall) ExternalInstall
		cause error
	}{
		{"short credential", func(i ExternalInstall) ExternalInstall { i.ManagementKey = "short"; return i }, nil},
		{"empty address", func(i ExternalInstall) ExternalInstall { i.BaseURL = ""; return i }, nil},
		{"no checkpoint", func(i ExternalInstall) ExternalInstall { i.Checkpoint = 0; return i }, nil},
		{"foreign identity", func(i ExternalInstall) ExternalInstall {
			i.Identity = "managed:sha256:" + strings.Repeat("c", 64)
			return i
		}, nil},
		{"unreadable digest", func(i ExternalInstall) ExternalInstall { i.ConfigSHA256 = ""; return i }, nil},
		{"rejected credential", func(i ExternalInstall) ExternalInstall { return i }, cpa.ErrManagementRejected},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			source := harness.cpa.source()
			service := NewExternal(nil, harness.keys, func(ctx context.Context) (ExternalInstall, error) {
				install, err := source(ctx)
				if err != nil {
					return ExternalInstall{}, err
				}
				return scenario.edit(install), nil
			})
			if scenario.cause != nil {
				service.externalSource = func(context.Context) (ExternalInstall, error) { return ExternalInstall{}, scenario.cause }
			}
			if _, _, err := service.ExternalTarget(ctx, 1); !errors.Is(err, ErrExternalUnavailable) &&
				!(scenario.cause != nil && errors.Is(err, scenario.cause)) {
				t.Fatalf("unresolved authority answered: %v", err)
			}
		})
	}
	if _, _, err := harness.service.ExternalTarget(ctx, 2); !errors.Is(err, ErrGeneration) {
		t.Fatalf("stale generation answered: %v", err)
	}
}

// Create stages a key and installs it on the user's CPA; rename is Manager-local,
// and the CPA is read back so the list never claims an effect it cannot see.
func TestExternalInstallRenameAndReadBack(t *testing.T) {
	harness := newExternalHarness(t)
	ctx := context.Background()
	id, secret := harness.staged(t, strings.Repeat("1", 32))

	state, err := harness.service.Apply(ctx, id, 1)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !slices.Contains(harness.cpa.keys, secret) {
		t.Fatal("the user's CPA did not receive the staged key")
	}
	if !state.ManagerStaged || !state.Attempted || !state.CPAConfigured || state.RuntimeState != "effective_observed" {
		t.Fatalf("apply state = %+v", state)
	}
	// A replay of the same key must not duplicate the credential.
	before := harness.cpa.writes
	if _, err = harness.service.Apply(ctx, id, 1); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if harness.cpa.writes != before {
		t.Fatal("a key the CPA already carried was written again")
	}
	if _, err := harness.service.ExternalKeyPresenceOf(ctx, "ignored"); !errors.Is(err, freshkeys.ErrRequest) {
		t.Fatalf("a malformed id was accepted: %v", err)
	}
	if present, err := harness.service.ExternalKeyPresenceOf(ctx, mustID(t, id)); err != nil || !present {
		t.Fatalf("read-back presence = %v %v", present, err)
	}
	renamed, err := harness.keys.Rename(ctx, id, "Production client", 1)
	if err != nil || renamed.NameRevision != 2 {
		t.Fatalf("rename = %+v %v", renamed, err)
	}
	if len(harness.cpa.keys) != 1 {
		t.Fatalf("rename reached the user's CPA: %v", harness.cpa.keys)
	}
}

// A revoke removes the live credential from the user's CPA and proves it; the
// last one is refused before anything is attempted, because removing it would
// leave the user's CPA accepting unauthenticated clients.
func TestExternalRevokeAndLastKeyProtection(t *testing.T) {
	harness := newExternalHarness(t)
	ctx := context.Background()
	target, targetSecret := harness.staged(t, strings.Repeat("2", 32))
	keeper, keeperSecret := harness.staged(t, strings.Repeat("3", 32))
	if _, err := harness.service.Apply(ctx, target, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.service.Apply(ctx, keeper, 1); err != nil {
		t.Fatal(err)
	}
	// Two credentials: revoking either is allowed.
	result, err := harness.service.Prepare(ctx, strings.Repeat("4", 32), target, keeper, "revoke", 1)
	if err != nil {
		t.Fatalf("prepare revoke: %v", err)
	}
	if result.Operation == nil || result.NextStep != "revoke" {
		t.Fatalf("prepared operation = %+v", result)
	}
	result, err = harness.service.ContinueOperation(ctx, result.Operation.ID, 1)
	if err != nil {
		t.Fatalf("continue revoke: %v", err)
	}
	if slices.Contains(harness.cpa.keys, targetSecret) {
		t.Fatal("the revoked credential is still carried by the user's CPA")
	}
	if !slices.Contains(harness.cpa.keys, keeperSecret) {
		t.Fatal("the keeper credential was lost")
	}
	if result.Operation.Outcome != "committed" {
		t.Fatalf("revoke outcome = %q", result.Operation.Outcome)
	}

	// Replace installs the new credential and then removes the old one.
	replacement, replacementSecret := harness.staged(t, strings.Repeat("5", 32))
	result, err = harness.service.Prepare(ctx, strings.Repeat("6", 32), keeper, replacement, "replace", 1)
	if err != nil {
		t.Fatalf("prepare replace: %v", err)
	}
	for result.NextStep != "" && result.Operation != nil && result.Operation.Outcome != "committed" {
		result, err = harness.service.ContinueOperation(ctx, result.Operation.ID, 1)
		if err != nil {
			t.Fatalf("continue replace: %v", err)
		}
	}
	if !slices.Contains(harness.cpa.keys, replacementSecret) {
		t.Fatal("the replacement credential is not carried")
	}
	if slices.Contains(harness.cpa.keys, keeperSecret) {
		t.Fatal("the replaced credential is still carried")
	}

	// One credential left: revoking it is refused and nothing is attempted.
	sole, _ := harness.staged(t, strings.Repeat("7", 32))
	spare, _ := harness.staged(t, strings.Repeat("8", 32))
	if _, err := harness.service.Apply(ctx, sole, 1); err != nil {
		t.Fatal(err)
	}
	harness.cpa.keys = []string{replacementSecret}
	harness.cpa.writes = 0
	if _, err = harness.service.Prepare(ctx, strings.Repeat("9", 32), replacement, spare, "revoke", 1); !errors.Is(err, ErrLastKey) {
		t.Fatalf("last key revoke = %v", err)
	}
	if harness.cpa.writes != 0 {
		t.Fatal("a refused revoke reached the user's CPA")
	}
}

// A 401 from the user's CPA is the typed blocker on every External write: it is
// reported as such, never retried, and never followed by another write.
func TestExternalRejectedManagementKeyIsNeverRetried(t *testing.T) {
	harness := newExternalHarness(t)
	ctx := context.Background()
	id, _ := harness.staged(t, strings.Repeat("a", 32))
	harness.cpa.rejectEvery = true
	state, err := harness.service.Apply(ctx, id, 1)
	if !errors.Is(err, cpa.ErrManagementRejected) {
		t.Fatalf("apply on a rejected credential = %v", err)
	}
	if state.RuntimeState != "effect_unknown" {
		t.Fatalf("a rejected credential claimed an effect: %+v", state)
	}
	// The management credential is refused before the list is even read, so no
	// write is attempted and none is retried.
	if harness.cpa.writes != 0 {
		t.Fatalf("a rejected credential still wrote: %d", harness.cpa.writes)
	}
	// The authority itself reports the same typed blocker to the page.
	if _, _, err := harness.service.ExternalTarget(ctx, 1); !errors.Is(err, cpa.ErrManagementRejected) {
		t.Fatalf("authority on a rejected credential = %v", err)
	}
}

// A revoke whose write the CPA refuses never removes the credential and never
// resends the step: the journal stays reconcilable.
func TestExternalRevokeRejectionKeepsTheCredential(t *testing.T) {
	harness := newExternalHarness(t)
	ctx := context.Background()
	target, targetSecret := harness.staged(t, strings.Repeat("b", 32))
	keeper, _ := harness.staged(t, strings.Repeat("c", 32))
	if _, err := harness.service.Apply(ctx, target, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.service.Apply(ctx, keeper, 1); err != nil {
		t.Fatal(err)
	}
	harness.cpa.rejectOnce = true
	result, err := harness.service.Prepare(ctx, strings.Repeat("d", 32), target, keeper, "revoke", 1)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err = harness.service.ContinueOperation(ctx, result.Operation.ID, 1); !errors.Is(err, cpa.ErrManagementRejected) {
		t.Fatalf("refused revoke = %v", err)
	}
	if !slices.Contains(harness.cpa.keys, targetSecret) {
		t.Fatal("a refused write removed the credential")
	}
	writes := harness.cpa.writes
	if _, err = harness.service.ContinueOperation(ctx, result.Operation.ID, 1); err == nil {
		t.Fatal("an attempted step was resent")
	}
	if harness.cpa.writes != writes {
		t.Fatal("reconcile repeated the write")
	}
}

func mustID(t *testing.T, raw string) identity.APIKeyID {
	t.Helper()
	parsed, err := identity.ParseAPIKeyID(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
