package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFreshCanonicalResourcesReuseCPAMPServices(t *testing.T) {
	app := newInstallationTestApplication(t)
	for _, resource := range []string{"cpamp/dashboard", "cpamp/config", "cpamp/status"} {
		t.Run(resource, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/control/api/v2/"+resource, nil)
			req.Header.Set("Authorization", "Bearer "+strategyTestAdmin)
			response := httptest.NewRecorder()
			app.canonicalAPI(response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("resource %s returned %d: %s", resource, response.Code, response.Body.String())
			}
			if !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("resource %s was not JSON: %s", resource, response.Header().Get("Content-Type"))
			}
		})
	}
}

func TestFreshCanonicalResourcesRemainAdminProtected(t *testing.T) {
	app := newInstallationTestApplication(t)
	for _, resource := range []string{"cpamp/dashboard", "cpamp/config", "cpamp/status"} {
		req := httptest.NewRequest(http.MethodGet, "/control/api/v2/"+resource, nil)
		response := httptest.NewRecorder()
		app.canonicalAPI(response, req)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("resource %s accepted missing auth: %d", resource, response.Code)
		}
	}
}
