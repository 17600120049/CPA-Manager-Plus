package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithSupportPluginHeaderAnnouncesCapabilityOnSuccess(t *testing.T) {
	cases := []struct {
		name     string
		target   string
		status   int
		wantHead string
	}{
		// Full Web reads this header off any successful response to learn whether
		// the saved connection can serve plugins; the default install must say yes.
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
			}))
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
	}))
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
