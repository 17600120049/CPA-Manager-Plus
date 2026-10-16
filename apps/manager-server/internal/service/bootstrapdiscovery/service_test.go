package bootstrapdiscovery

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	bootstrap "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
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
		want  Result
	}{
		{name: "fresh", input: Input{Report: freshReport()}, want: Result{
			Available: true, Mode: bootstrap.Fresh, Reason: ReasonFreshClaimRequired,
		}},
		{name: "unqualified legacy", input: Input{Report: legacyReport("initialized")}, want: Result{
			Available: true, Mode: bootstrap.UnsupportedUpgrade, Reason: ReasonQualificationUnavailable, AuthenticationRequired: true,
		}},
		{name: "unverifiable legacy recovery", input: Input{Report: legacyReport("unverifiable")}, want: Result{
			Available: true, Mode: bootstrap.Recovery, Reason: ReasonHostInterventionRequired, RecoveryRequired: true,
		}},
		{name: "unsafe observation", input: Input{Report: legacyReport("initialized"), InspectionError: errors.New("source changed")}, want: Result{
			Reason: ReasonUnavailable,
		}},
		{name: "native schema is not completed", input: Input{Report: legacypreflight.Report{
			SourceKind: "native-v2", Observed: sqlite.LegacyInspection{SQLite: "observed", Consistency: "operator-asserted-cold", Files: []sqlite.InspectionFile{{Role: "database", State: "present"}}, BootstrapState: sqlite.BootstrapStateInspection{Valid: true, AdminCredentialPresent: true, AdminCredential: "initialized"}},
		}}, want: Result{
			Available: true, Mode: bootstrap.UnsupportedUpgrade, Reason: ReasonQualificationUnavailable, AuthenticationRequired: true,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Project(test.input); got != test.want {
				t.Fatalf("Project() = %#v, want %#v", got, test.want)
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
	if got.Mode != bootstrap.UnsupportedUpgrade || got.Reason != ReasonQualificationUnavailable || !got.Available {
		t.Fatalf("legacy state was mapped to a new terminal/fresh mode: %#v", got)
	}
}

func TestInvalidBootstrapWitnessFailsClosed(t *testing.T) {
	report := legacyReport("initialized")
	report.Observed.BootstrapState.Valid = false
	got := Project(Input{Report: report})
	if got.Available || got.Mode != "" || got.Reason != ReasonQualificationUnknown {
		t.Fatalf("invalid bootstrap witness was exposed: %#v", got)
	}
}

func TestHandlerCapturesMethodsAndCompletedTerminal(t *testing.T) {
	active := Handler{Result: Project(Input{Report: freshReport()})}
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
	completed := Handler{Result: Result{Available: true, Mode: bootstrap.Completed}}
	for _, method := range []string{http.MethodGet, http.MethodOptions, http.MethodPost} {
		res := httptest.NewRecorder()
		completed.ServeHTTP(res, httptest.NewRequest(method, Path, nil))
		if res.Code != http.StatusNotFound || res.Body.Len() != 0 {
			t.Fatalf("completed %s response=%d body=%q", method, res.Code, res.Body.String())
		}
	}
}

func TestUnavailableDoesNotManufactureMode(t *testing.T) {
	res := Handler{Result: Unavailable(ReasonQualificationUnknown)}
	recorder := httptest.NewRecorder()
	res.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, Path, nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", recorder.Code)
	}
	if recorder.Body.String() == "" || containsMode(recorder.Body.String()) {
		t.Fatalf("unavailable response disclosed mode: %s", recorder.Body.String())
	}
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
