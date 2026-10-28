package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeyapply"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
)

const externalAuthorityManagement = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// externalAuthorityCPA is a user-owned CPA answering the two management reads the
// External authority needs to derive its generation.
type externalAuthorityCPA struct {
	management string
	reads      atomic.Int32
	server     *httptest.Server
}

func newExternalAuthorityCPA(t *testing.T) *externalAuthorityCPA {
	t.Helper()
	f := &externalAuthorityCPA{management: externalAuthorityManagement}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.management {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v8/management/config/access/api-keys":
			f.reads.Add(1)
			_, _ = w.Write([]byte("[]"))
		case "/v8/management/config.yaml":
			f.reads.Add(1)
			_, _ = w.Write([]byte(`{"config-version":8}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

// externalAuthorityApp commits a Connect External checkpoint bound to the fake
// CPA and returns the application that administers it.
func externalAuthorityApp(t *testing.T, targetIdentity string) (*freshApplication, *externalAuthorityCPA) {
	t.Helper()
	app, closeApp := newA3TestApp(t)
	t.Cleanup(closeApp)
	cpaServer := newExternalAuthorityCPA(t)
	baseURL, err := cpa.NormalizeV8BaseURL(cpaServer.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := app.protector.ProtectString(cpaServer.management)
	if err != nil {
		t.Fatal(err)
	}
	record, err := app.store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if targetIdentity == "" {
		targetIdentity = freshkeyapply.ExternalIdentity(baseURL)
	}
	if _, err = app.store.Update(t.Context(), record.Operation.Identity, record.Operation.Revision, func(r *freshstore.Record) error {
		r.Operation.Strategy = bootstrap.FreshStrategyCheckpoint{
			Strategy: bootstrap.ConnectExternal, Stage: "committed",
			TargetIdentity: targetIdentity, TargetGeneration: 1,
		}
		r.Connection = &model.ManagerCPAConnectionConfig{CPABaseURL: baseURL, ManagementKey: protected}
		r.Operation.ProductComplete = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return app, cpaServer
}

// TestExternalAuthorityResolvesTheCommittedConnection is the positive half: the
// committed External checkpoint plus the protected Manager connection resolve to
// the user's CPA. The authority is those two durable facts alone, so resolving it
// never contacts the CPA and an outage there cannot hide the target.
func TestExternalAuthorityResolvesTheCommittedConnection(t *testing.T) {
	app, cpaServer := externalAuthorityApp(t, "")
	endpoint, management, err := app.keyApply.ExternalTarget(t.Context(), 1)
	if err != nil {
		t.Fatalf("resolve committed External target: %v", err)
	}
	if endpoint != cpaServer.server.URL {
		t.Fatalf("endpoint = %q, want %q", endpoint, cpaServer.server.URL)
	}
	if management != externalAuthorityManagement {
		t.Fatal("the protected management credential was not decrypted")
	}
	if cpaServer.reads.Load() != 0 {
		t.Fatalf("resolving the authority contacted the user's CPA %d times", cpaServer.reads.Load())
	}
	// A checkpoint the page does not hold is a generation mismatch, not a target.
	if _, _, err = app.keyApply.ExternalTarget(t.Context(), 2); !errors.Is(err, freshkeyapply.ErrGeneration) {
		t.Fatalf("stale generation = %v", err)
	}
}

// TestExternalAuthorityRefusesAConnectionTheCheckpointDoesNotName proves the
// committed target and the live connection must be the same CPA. Without the
// digest comparison a connection the user edited to another CPA would be
// followed silently — a rebind this path must never perform — so the authority
// refuses and never contacts that CPA.
func TestExternalAuthorityRefusesAConnectionTheCheckpointDoesNotName(t *testing.T) {
	app, cpaServer := externalAuthorityApp(t, "external:sha256:"+strings.Repeat("c", 64))
	if _, _, err := app.keyApply.ExternalTarget(t.Context(), 1); !errors.Is(err, freshkeyapply.ErrExternalUnavailable) {
		t.Fatalf("foreign connection identity = %v", err)
	}
	if cpaServer.reads.Load() != 0 {
		t.Fatal("a connection the checkpoint does not name was contacted")
	}
}

// TestExternalKeyListNeverReadsACPAForAnotherProfile proves the list read-back is
// gated on the External discriminator. A Fresh application without a committed
// External checkpoint stages keys exactly as before: it must answer 200 with the
// staged keys, not fail because there is no CPA to read.
func TestExternalKeyListNeverReadsACPAForAnotherProfile(t *testing.T) {
	app, closeApp := newA3TestApp(t)
	t.Cleanup(closeApp)
	if app.keyApply.Profile() != freshkeyapply.ProfileExternal {
		t.Fatalf("profile = %q", app.keyApply.Profile())
	}
	created := app.keyApplyKeys(t, strings.Repeat("a", 32))
	request := httptest.NewRequest(http.MethodGet, "/control/api/v2/api-keys", nil)
	request.Header.Set("Authorization", "Bearer "+strategyTestAdmin)
	response := httptest.NewRecorder()
	app.canonicalAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list without a committed checkpoint = %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), string(created.Key.ID)) {
		t.Fatal("the staged key is missing from the list")
	}
}

// keyApplyKeys stages one key through the Manager-local record.
func (a *freshApplication) keyApplyKeys(t *testing.T, requestID string) freshkeys.Result {
	t.Helper()
	result, _, err := a.keys.Create(context.Background(), requestID)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
