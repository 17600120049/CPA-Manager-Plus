package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func gatewayRequest(t *testing.T, app *freshApplication, method, resource, body string, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	request := httptest.NewRequest(method, "/control/api/v2/"+resource, reader)
	request.Header.Set("Authorization", "Bearer "+strategyTestAdmin)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	app.canonicalAPI(response, request)
	return response
}

func errorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body %q: %v", response.Body.String(), err)
	}
	return payload.Code
}

// TestGatewayConfigRequiresAnAuthority proves the page fails closed when this
// installation holds no configuration authority at all.
func TestGatewayConfigRequiresAnAuthority(t *testing.T) {
	app := newInstallationTestApplication(t)
	markFreshProductComplete(t, app)
	app.gatewayConfig = nil
	response := gatewayRequest(t, app, http.MethodGet, "gateway/config", "", "")
	if response.Code != http.StatusConflict || errorCode(t, response) != "config_authority_unavailable" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// TestGatewayConfigSurfaceRejectsAnythingOutsideTheContract covers the request
// shape, the method and the resource surface before any CPA write is possible.
func TestGatewayConfigSurfaceRejectsAnythingOutsideTheContract(t *testing.T) {
	app := newInstallationTestApplication(t)
	markFreshProductComplete(t, app)
	app.gatewayConfig = nil
	requestID := strings.Repeat("ab", 16)
	expected := `{"generation":"7","baseline":"` + strings.Repeat("a", 64) + `"}`
	cases := []struct {
		name        string
		method      string
		resource    string
		body        string
		contentType string
		status      int
		code        string
		allow       string
	}{
		{"wrong content type", http.MethodPost, "gateway/config/groups/retry", `{}`, "text/plain", http.StatusUnsupportedMediaType, "", ""},
		{"missing content type", http.MethodPost, "gateway/config/groups/retry", `{}`, "", http.StatusUnsupportedMediaType, "", ""},
		{"unknown body key", http.MethodPost, "gateway/config/groups/retry", `{"requestId":"` + requestID + `","expected":` + expected + `,"edits":[],"yaml":"x"}`, "application/json", http.StatusBadRequest, "invalid_config_request", ""},
		{"unknown expectation", http.MethodPost, "gateway/config/groups/retry", `{"requestId":"` + requestID + `","expected":{"generation":"7","baseline":"` + strings.Repeat("a", 64) + `","sha256":"x"},"edits":[]}`, "application/json", http.StatusBadRequest, "invalid_config_request", ""},
		{"unknown group", http.MethodPost, "gateway/config/groups/nope", `{"requestId":"` + requestID + `","expected":` + expected + `,"edits":[{"path":"routing.retry.request-retry","action":"set","value":3}]}`, "application/json", http.StatusBadRequest, "invalid_config_request", ""},
		{"foreign leaf", http.MethodPost, "gateway/config/groups/retry", `{"requestId":"` + requestID + `","expected":` + expected + `,"edits":[{"path":"server.port","action":"set","value":9000}]}`, "application/json", http.StatusBadRequest, "invalid_config_request", ""},
		{"proxy credentials", http.MethodPost, "gateway/config/groups/request", `{"requestId":"` + requestID + `","expected":` + expected + `,"edits":[{"path":"requests.proxy-url","action":"set","value":"http://u:p@proxy.test:8080"}]}`, "application/json", http.StatusBadRequest, "invalid_config_request", ""},
		{"duplicate rule path", http.MethodPost, "gateway/config/rules/filter", `{"requestId":"` + requestID + `","expected":` + expected + `,"rules":[{"models":[{"name":"*"}],"params":["a","a"]}]}`, "application/json", http.StatusBadRequest, "invalid_config_request", ""},
		{"wrong group method", http.MethodGet, "gateway/config/groups/retry", "", "", http.StatusMethodNotAllowed, "", http.MethodPost},
		{"wrong family method", http.MethodPut, "gateway/config/rules/filter", `{}`, "application/json", http.StatusMethodNotAllowed, "", http.MethodPost},
		{"wrong read method", http.MethodPost, "gateway/config", `{}`, "application/json", http.StatusMethodNotAllowed, "", http.MethodGet},
		{"unknown resource", http.MethodGet, "gateway/config/unknown", "", "", http.StatusNotFound, "", ""},
		{"field resource removed", http.MethodGet, "gateway/config/requests/streaming/keepalive-seconds", "", "", http.StatusNotFound, "", ""},
		{"operation without id", http.MethodGet, "gateway/config/operations", "", "", http.StatusNotFound, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := gatewayRequest(t, app, tc.method, tc.resource, tc.body, tc.contentType)
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.code != "" && errorCode(t, response) != tc.code {
				t.Fatalf("body=%s", response.Body.String())
			}
			if tc.allow != "" && response.Header().Get("Allow") != tc.allow {
				t.Fatalf("allow=%q", response.Header().Get("Allow"))
			}
		})
	}
	// A query string and a non-canonical escaping are never a resource selector.
	for _, target := range []string{"gateway/config?path=routing", "gateway/config/groups/retry%2F"} {
		request := httptest.NewRequest(http.MethodGet, "/control/api/v2/"+target, nil)
		request.Header.Set("Authorization", "Bearer "+strategyTestAdmin)
		response := httptest.NewRecorder()
		app.canonicalAPI(response, request)
		if response.Code != http.StatusBadRequest && response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d", target, response.Code)
		}
	}
}

// TestGatewayConfigOperationReadsNeedAnAuthority covers the operation surface: an
// unjournaled request ID is not an unknown result, and the page never writes.
func TestGatewayConfigOperationReadsNeedAnAuthority(t *testing.T) {
	app := newInstallationTestApplication(t)
	markFreshProductComplete(t, app)
	app.gatewayConfig = nil
	response := gatewayRequest(t, app, http.MethodGet, "gateway/config/operations/"+strings.Repeat("ab", 16), "", "")
	if response.Code != http.StatusConflict || errorCode(t, response) != "config_authority_unavailable" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
