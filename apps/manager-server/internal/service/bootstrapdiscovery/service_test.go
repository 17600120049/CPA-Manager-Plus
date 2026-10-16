package bootstrapdiscovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	bootstrap "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

func freshReport() legacypreflight.Report {
	return legacypreflight.Report{
		SourceKind: "fresh",
		Observed: sqlite.LegacyInspection{
			SQLite:         "not-inspected",
			Files:          []sqlite.InspectionFile{{Role: "database", State: "missing"}},
			BootstrapState: sqlite.BootstrapStateInspection{Valid: true, AdminCredential: "uninitialized"},
		},
	}
}

func legacyReport(admin string) legacypreflight.Report {
	return legacypreflight.Report{
		SourceKind: "legacy-manager",
		Observed: sqlite.LegacyInspection{
			SQLite:      "observed",
			Consistency: "operator-asserted-cold",
			Files:       []sqlite.InspectionFile{{Role: "database", State: "present", Size: 1024}},
			BootstrapState: sqlite.BootstrapStateInspection{
				Present:                false,
				Valid:                  true,
				AdminCredentialPresent: admin != "uninitialized",
				AdminCredential:        admin,
			},
		},
	}
}

func TestProjectEvidenceBoundedModes(t *testing.T) {
	tests := []struct {
		name  string
		input Input
		want  bootstrap.PublicDiscovery
	}{
		{name: "fresh", input: Input{Report: freshReport()}, want: bootstrap.PublicDiscovery{
			ContractVersion: bootstrap.PublicDiscoveryContractVersion,
			Status:          bootstrap.PublicFreshClaimRequired,
			Mode:            modePtr(bootstrap.Fresh), AuthenticationRequired: true,
			AuthEntry: bootstrap.AuthEntryFreshClaim, Reason: bootstrap.ReasonFreshClaimRequired,
		}},
		{name: "unqualified legacy", input: Input{Report: legacyReport("initialized")}, want: bootstrap.PublicDiscovery{
			ContractVersion: bootstrap.PublicDiscoveryContractVersion,
			Status:          bootstrap.PublicUnsupportedUpgrade,
			Mode:            modePtr(bootstrap.UnsupportedUpgrade), AuthenticationRequired: true,
			AuthEntry: bootstrap.AuthEntryAdminSession, Reason: bootstrap.ReasonQualificationUnavailable,
		}},
		{name: "unverifiable legacy recovery", input: Input{Report: legacyReport("unverifiable")}, want: bootstrap.PublicDiscovery{
			ContractVersion: bootstrap.PublicDiscoveryContractVersion,
			Status:          bootstrap.PublicRecoveryRequired,
			Mode:            modePtr(bootstrap.Recovery), AuthEntry: bootstrap.AuthEntryHostIntervention,
			Reason: bootstrap.ReasonRecoveryHostIntervention,
		}},
		{name: "unsafe observation", input: Input{Report: legacyReport("initialized"), InspectionError: errors.New("source changed")}, want: bootstrap.PublicDiscovery{
			ContractVersion: bootstrap.PublicDiscoveryContractVersion,
			Status:          bootstrap.PublicUnavailable,
			AuthEntry:       bootstrap.AuthEntryUnavailable, Reason: bootstrap.ReasonObservationUnavailable,
		}},
		{name: "native schema is not completed", input: Input{Report: legacypreflight.Report{
			SourceKind: "native-v2", Observed: sqlite.LegacyInspection{SQLite: "observed", Consistency: "operator-asserted-cold", Files: []sqlite.InspectionFile{{Role: "database", State: "present"}}, BootstrapState: sqlite.BootstrapStateInspection{Valid: true, AdminCredentialPresent: true, AdminCredential: "initialized"}},
		}}, want: bootstrap.PublicDiscovery{
			ContractVersion: bootstrap.PublicDiscoveryContractVersion,
			Status:          bootstrap.PublicUnsupportedUpgrade,
			Mode:            modePtr(bootstrap.UnsupportedUpgrade), AuthenticationRequired: true,
			AuthEntry: bootstrap.AuthEntryAdminSession, Reason: bootstrap.ReasonQualificationUnavailable,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Project(test.input); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Project() = %#v, want %#v", got, test.want)
			}
			if err := test.want.Validate(); err != nil {
				t.Fatalf("expected discovery is invalid: %v", err)
			}
		})
	}
}

func TestInspectedCredentialSelectsRecoveryOrAdminSessionWithoutWrites(t *testing.T) {
	const adminKey = "cpamp_owned_credential_secret"
	credential, err := security.NewAdminCredential(adminKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*model.AdminCredential)
		raw    string
		valid  bool
	}{
		{name: "real credential", valid: true},
		{name: "legacy iterations", change: func(c *model.AdminCredential) { c.Iterations = 0 }, valid: true},
		{name: "short digest", change: func(c *model.AdminCredential) { c.KeyHash = "a2V5" }},
		{name: "unsupported version", change: func(c *model.AdminCredential) { c.Version = 2 }},
		{name: "whitespace encoding", change: func(c *model.AdminCredential) { c.Salt = " " + c.Salt }},
		{name: "invalid JSON", raw: `{"salt":"OWNED_SECRET`},
		{name: "wrong field type", raw: `{"version":1,"salt":123,"keyHash":"OWNED_SECRET"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := legacypreflight.DefaultPaths(filepath.Join(dir, "source.sqlite"))
			current := credential
			if test.change != nil {
				test.change(&current)
			}
			raw, err := json.Marshal(current)
			if err != nil {
				t.Fatal(err)
			}
			if test.raw != "" {
				raw = []byte(test.raw)
			}
			db, err := sql.Open("sqlite", paths.Database)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`create table settings(key text primary key,value text); create table usage_events(id integer primary key)`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`insert into settings values('admin_credential_v1',?)`, string(raw)); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(paths.Database)
			if err != nil {
				t.Fatal(err)
			}
			report, err := legacypreflight.Inspect(context.Background(), paths, legacypreflight.Options{SourceStopped: true})
			if err != nil {
				t.Fatal(err)
			}
			result := Project(Input{Report: report})
			wantMode, wantAuth := bootstrap.Recovery, bootstrap.AuthEntryHostIntervention
			if test.valid {
				wantMode, wantAuth = bootstrap.UnsupportedUpgrade, bootstrap.AuthEntryAdminSession
			}
			if result.Mode == nil || *result.Mode != wantMode || result.AuthEntry != wantAuth {
				t.Fatalf("credential classification=%+v want %s/%s", result, wantMode, wantAuth)
			}
			if security.VerifyAdminKey(current, adminKey) != test.valid && test.raw == "" {
				t.Fatal("fixture disagrees with real authentication")
			}
			response := httptest.NewRecorder()
			Handler{Discovery: result}.ServeHTTP(response, httptest.NewRequest(http.MethodGet, Path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{adminKey, credential.Salt, credential.KeyHash, "OWNED_SECRET"} {
				if strings.Contains(string(encoded), secret) || strings.Contains(response.Body.String(), secret) {
					t.Fatal("credential leaked into observation/discovery")
				}
			}
			after, err := os.ReadFile(paths.Database)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("inspection changed source: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("inspection created source-adjacent artifacts: entries=%v err=%v", entries, err)
			}
		})
	}
}

func TestLegacyBootstrapStateDoesNotMapToCompletedOrFresh(t *testing.T) {
	report := legacyReport("initialized")
	report.Observed.BootstrapState.Present = true
	report.Observed.BootstrapState.ProjectInitialized = true
	report.Observed.BootstrapState.HistoricalData = true
	got := Project(Input{Report: report})
	if got.Mode == nil || *got.Mode != bootstrap.UnsupportedUpgrade || got.Reason != bootstrap.ReasonQualificationUnavailable ||
		got.Status != bootstrap.PublicUnsupportedUpgrade || !got.AuthenticationRequired {
		t.Fatalf("legacy state was mapped to a new terminal/fresh mode: %#v", got)
	}
}

func TestFreshRequiresExplicitNoV1Witness(t *testing.T) {
	report := freshReport()
	report.Observed.BootstrapState = sqlite.BootstrapStateInspection{}
	got := Project(Input{Report: report})
	if got.Status != bootstrap.PublicUnavailable || got.Mode != nil || got.Reason != bootstrap.ReasonObservationUnavailable {
		t.Fatalf("missing fresh witness was treated as authoritative: %#v", got)
	}
}

func TestInvalidBootstrapWitnessFailsClosed(t *testing.T) {
	report := legacyReport("initialized")
	report.Observed.BootstrapState.Valid = false
	got := Project(Input{Report: report})
	if got.Status != bootstrap.PublicUnavailable || got.Mode != nil || got.Reason != bootstrap.ReasonObservationUnavailable {
		t.Fatalf("invalid bootstrap witness was exposed: %#v", got)
	}
}

func TestHandlerCapturesMethodsAndCompletedTerminal(t *testing.T) {
	active := Handler{Discovery: Project(Input{Report: freshReport()})}
	for _, method := range []string{http.MethodGet, http.MethodOptions} {
		req := httptest.NewRequest(method, Path, nil)
		res := httptest.NewRecorder()
		active.ServeHTTP(res, req)
		want := http.StatusOK
		if method == http.MethodOptions {
			want = http.StatusNoContent
		}
		if res.Code != want {
			t.Fatalf("%s status=%d want=%d", method, res.Code, want)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		res := httptest.NewRecorder()
		active.ServeHTTP(res, httptest.NewRequest(method, Path, nil))
		if res.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status=%d want 405", method, res.Code)
		}
	}
	completedMode := bootstrap.Completed
	completed := Handler{Discovery: bootstrap.PublicDiscovery{Mode: &completedMode}}
	for _, method := range []string{http.MethodGet, http.MethodOptions, http.MethodPost} {
		res := httptest.NewRecorder()
		completed.ServeHTTP(res, httptest.NewRequest(method, Path, nil))
		if res.Code != http.StatusNotFound || res.Body.Len() != 0 {
			t.Fatalf("completed %s response=%d body=%q", method, res.Code, res.Body.String())
		}
	}
}

func TestUnavailableDoesNotManufactureMode(t *testing.T) {
	res := Handler{Discovery: Unavailable()}
	recorder := httptest.NewRecorder()
	res.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, Path, nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", recorder.Code)
	}
	if recorder.Body.String() == "" || containsMode(recorder.Body.String()) {
		t.Fatalf("unavailable response disclosed mode: %s", recorder.Body.String())
	}
}

func TestHandlerRedactsInspectionDetails(t *testing.T) {
	result := Project(Input{
		Report:          legacyReport("initialized"),
		InspectionError: errors.New("sqlite /private/manager.sqlite cpa=https://cpa.invalid key=admin-secret archive=/private/archive backup=receipt port=:18317 resource=internal-ref"),
	})
	recorder := httptest.NewRecorder()
	(Handler{Discovery: result}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, Path, nil))
	body := recorder.Body.String()
	for _, forbidden := range []string{
		"controlBasePath", "cpa.invalid", "admin-secret", "/private/manager.sqlite",
		"/private/archive", "receipt", ":18317", "sqlite", "internal-ref",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("discovery leaked %q: %s", forbidden, body)
		}
	}
}

func modePtr(value bootstrap.Mode) *bootstrap.Mode {
	return &value
}

func containsMode(body string) bool {
	return len(body) > 0 && (body[0] == '{' && stringContains(body, `"mode"`))
}

func stringContains(value, target string) bool {
	for i := 0; i+len(target) <= len(value); i++ {
		if value[i:i+len(target)] == target {
			return true
		}
	}
	return false
}
