package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWithSupportPluginHeaderAnnouncesCapabilityOnSuccess(t *testing.T) {
	cases := []struct {
		name     string
		target   string
		status   int
		wantHead string
	}{
		// Full Web reads this header off any successful response to learn whether
		// the saved connection can serve plugins. This test holds the proven
		// capability constant so it isolates the status/wiring rule; the
		// capability itself is proven separately below.
		{"management list", "/v0/management/plugins", http.StatusOK, "1"},
		{"bridge resource", "/console/api/v2/cpamp/config", http.StatusOK, "1"},
		{"created", "/v0/management/plugin-store/official/install", http.StatusCreated, "1"},
		{"no content", "/v0/management/plugins/demo/enabled", http.StatusNoContent, "1"},
		// A failure must not claim the deployment can serve plugins.
		{"unauthorized", "/v0/management/plugins", http.StatusUnauthorized, ""},
		{"conflict", "/v0/management/plugins/demo/enabled", http.StatusConflict, ""},
		{"server error", "/v0/management/plugins", http.StatusServiceUnavailable, ""},
		// Plugin resources keep their own sandbox contract and must not advertise
		// management capability to plugin code.
		{"plugin resource", "/v0/resource/plugins/demo/index.html", http.StatusOK, ""},
		// Private internal namespaces are not a public contract.
		{"internal namespace", "/internal/routing/fresh-authority", http.StatusNoContent, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := withSupportPluginHeader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(testCase.status)
			}), func(*http.Request) bool { return true })
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, testCase.target, nil))
			if got := recorder.Header().Get(supportPluginHeader); got != testCase.wantHead {
				t.Fatalf("header = %q, want %q", got, testCase.wantHead)
			}
		})
	}
}

// A handler that writes a body without an explicit status commits 200, and Full
// Web must still see the capability on it.
func TestWithSupportPluginHeaderImpliedStatus(t *testing.T) {
	handler := withSupportPluginHeader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}), func(*http.Request) bool { return true })
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v0/management/plugins", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get(supportPluginHeader); got != "1" {
		t.Fatalf("header = %q, want 1", got)
	}
	if recorder.Body.String() != `{"ok":true}` {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestPluginResourceRequestPredicate(t *testing.T) {
	cases := []struct {
		name   string
		target string
		want   bool
	}{
		{"canonical", "/v0/resource/plugins/demo/index.html", true},
		{"with query", "/v0/resource/plugins/demo/index.html?v=7", true},
		{"nested", "/v0/resource/plugins/demo/assets/app.js", true},
		{"encoded separator", "/v0/resource/plugins%2fdemo/index.html", false},
		{"dot segment", "/v0/resource/plugins/../console", false},
		{"doubled separator", "/v0/resource/plugins//demo/index.html", false},
		{"management path", "/v0/management/plugins", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, testCase.target, nil)
			if got := pluginResourceRequest(request); got != testCase.want {
				t.Fatalf("predicate = %v, want %v", got, testCase.want)
			}
		})
	}
}

// A connection that cannot serve plugins must not be advertised as able to.
// The header is the navigation's only input, so a route existing is not enough:
// an External or Private connection without a proven v8 plugin endpoint would
// otherwise show plugin management for a CPA that has none.
func TestSupportPluginHeaderRequiresProvenCapability(t *testing.T) {
	handler := func(capable bool) http.Handler {
		return withSupportPluginHeader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}), func(*http.Request) bool { return capable })
	}

	proven := httptest.NewRecorder()
	handler(true).ServeHTTP(proven, httptest.NewRequest(http.MethodGet, "/v0/management/plugins", nil))
	if got := proven.Header().Get(supportPluginHeader); got != "1" {
		t.Fatalf("proven connection header = %q, want 1", got)
	}

	unproven := httptest.NewRecorder()
	handler(false).ServeHTTP(unproven, httptest.NewRequest(http.MethodGet, "/v0/management/plugins", nil))
	if got := unproven.Header().Get(supportPluginHeader); got != "" {
		t.Fatalf("unproven connection advertised plugin support: %q", got)
	}
	// The request still succeeded: capability is not authorization.
	if unproven.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", unproven.Code)
	}
}

// The probe only trusts a fresh answer, asks once per window, and forgets on
// demand so a changed connection is not judged by the previous one's answer.
func TestPluginCapabilityProbeCachesAndForgets(t *testing.T) {
	answers := []bool{false, true}
	calls := 0
	probe := newPluginCapabilityProbe(time.Hour, func(context.Context) bool {
		value := answers[min(calls, len(answers)-1)]
		calls++
		return value
	})
	ctx := context.Background()
	if probe.capable(ctx) {
		t.Fatal("first answer should be false")
	}
	// Cached: the second read must not ask again.
	if calls != 1 {
		t.Fatalf("probe calls = %d after one read", calls)
	}
	if probe.capable(ctx) {
		t.Fatal("cached answer should still be false")
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d after a cached read", calls)
	}
	probe.forget()
	if !probe.capable(ctx) {
		t.Fatal("forget should re-prove and see the new answer")
	}
	if calls != 2 {
		t.Fatalf("probe calls = %d after forget", calls)
	}
}

// A probe with nothing to ask proves nothing: the default is not capable, so a
// wiring mistake narrows the navigation instead of widening it.
func TestPluginCapabilityProbeWithoutEvidenceIsNotCapable(t *testing.T) {
	var unset *pluginCapabilityProbe
	if unset.capable(context.Background()) {
		t.Fatal("a nil probe claimed capability")
	}
	if newPluginCapabilityProbe(time.Hour, nil).capable(context.Background()) {
		t.Fatal("a probe without evidence claimed capability")
	}
}
