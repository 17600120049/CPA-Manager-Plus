package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRejectCrossSiteAdmin(t *testing.T) {
	exempt := func(path string) bool { return path == "/v0/resource/plugins/demo/page.html" }
	reached := false
	handler := RejectCrossSiteAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}), exempt)

	cases := []struct {
		name    string
		path    string
		site    string
		dest    string
		want    int
		reached bool
	}{
		// A sandboxed plugin document is an opaque origin: the browser labels its
		// admin API calls cross-site. This is the case the middleware exists for.
		{"plugin fetch to admin api", "/v0/management/config", "cross-site", "empty", http.StatusForbidden, false},
		{"plugin fetch without dest", "/v0/management/plugins", "cross-site", "", http.StatusForbidden, false},
		{"plugin xhr to canonical api", "/console/api/v2/cpamp/config", "cross-site", "empty", http.StatusForbidden, false},
		// The admin UI's own calls must keep working.
		{"admin same-origin fetch", "/v0/management/config", "same-origin", "empty", http.StatusOK, true},
		{"admin same-site fetch", "/v0/management/config", "same-site", "empty", http.StatusOK, true},
		{"user typed url", "/v0/management/config", "none", "empty", http.StatusOK, true},
		// A non-browser client sends no Fetch Metadata at all; the transport's
		// existing contract keeps it working.
		{"cli client", "/v0/management/config", "", "", http.StatusOK, true},
		// A cross-site navigation, image or script is not an admin data call and
		// must not become a hard failure.
		{"cross-site navigation", "/console/", "cross-site", "document", http.StatusOK, true},
		{"cross-site image", "/favicon.ico", "cross-site", "image", http.StatusOK, true},
		// Plugin resources carry their own fail-closed policy, which admits a
		// cross-site request from the sandbox.
		{"plugin resource exempt", "/v0/resource/plugins/demo/page.html", "cross-site", "empty", http.StatusOK, true},
		{"exempt path same-origin", "/v0/resource/plugins/demo/page.html", "same-origin", "script", http.StatusOK, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			reached = false
			request := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			if testCase.site != "" {
				request.Header.Set("Sec-Fetch-Site", testCase.site)
			}
			if testCase.dest != "" {
				request.Header.Set("Sec-Fetch-Dest", testCase.dest)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != testCase.want {
				t.Fatalf("status = %d, want %d", recorder.Code, testCase.want)
			}
			if reached != testCase.reached {
				t.Fatalf("inner handler reached = %v, want %v", reached, testCase.reached)
			}
			if recorder.Code == http.StatusForbidden {
				if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
					t.Fatalf("rejection cacheable: %q", got)
				}
			}
		})
	}
}

func TestRejectCrossSiteAdminWithoutExemption(t *testing.T) {
	handler := RejectCrossSiteAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), nil)
	request := httptest.NewRequest(http.MethodGet, "/v0/resource/plugins/demo/page.html", nil)
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}
