package cpa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestConfigFieldReconcileAcceptsPreTableScope covers an operation the Runtime
// reserved before the page became group-based: it reports the legacy scope, so
// the Manager must bind the original operation by field, never by raw scope.
func TestConfigFieldReconcileAcceptsPreTableScope(t *testing.T) {
	requestID := strings.Repeat("cd", 16)
	for _, tc := range []struct {
		scope string
		path  string
		value any
		op    configOperation
	}{
		{"request-retry", "routing.retry.request-retry", 3, configOperation{Retry: 3}},
		{"max-retry-interval", "routing.retry.max-retry-interval", 30, configOperation{MaxRetryInterval: 30, MaxRetryIntervalSet: true}},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			source := ConfigFieldSource{SourceID: "isolated-source-1234", Generation: "7", Revision: 2, SHA256: strings.Repeat("a", 64)}
			operation := tc.op
			operation.ID, operation.Scope, operation.Generation, operation.State = requestID, tc.scope, "7", "unknown"
			v := configObservation{SourceID: source.SourceID, Generation: source.Generation, Revision: 3, SHA256: source.SHA256, State: "unknown", Installed: true, Operation: operation}
			commits := 0
			tr := &Transport{configurationAuthority: &privateConfigClient{
				grant: func() (configGrant, error) {
					return configGrant{SourceID: source.SourceID, Generation: source.Generation}, nil
				},
				call: func(_ context.Context, q configRequest) (configObservation, error) {
					switch q.Action {
					case "observe":
						return v, nil
					case "commit":
						if q.OperationID != requestID || q.ExpectedRevision != v.Revision {
							return configObservation{}, ErrSourceConflict
						}
						commits++
						v.State, v.Operation.State = "persisted", "persisted"
						return v, nil
					default:
						return configObservation{}, ErrSourceConflict
					}
				},
			}}
			got, err := tr.ReconcileConfigField(t.Context(), source, requestID, tc.path, tc.value)
			if err != nil {
				t.Fatalf("legacy %s operation was not reconciled: %v", tc.scope, err)
			}
			if got.OperationState != "persisted" || got.OperationPath != tc.path || commits != 1 {
				t.Fatalf("reconciled to %+v after %d commits", got, commits)
			}
		})
	}
}

// TestReadConfigFieldValueRetainsPresence is the read-back half of legacy
// reconciliation: a missing key, an explicit null and a present scalar are three
// different observations and none of them is a write.
func TestReadConfigFieldValueRetainsPresence(t *testing.T) {
	for _, tc := range []struct {
		path     string
		body     string
		status   int
		presence string
		value    any
	}{
		{"routing.retry.request-retry", "", http.StatusNotFound, "missing", nil},
		{"routing.retry.request-retry", "null", http.StatusOK, "null", nil},
		{"routing.retry.request-retry", "0", http.StatusOK, "present", 0},
		{"routing.retry.request-retry", "42", http.StatusOK, "present", 42},
		{"requests.passthrough-headers", "true", http.StatusOK, "present", true},
		{"requests.passthrough-headers", "1", http.StatusOK, "unknown", nil},
		{"routing.retry.request-retry", `"0"`, http.StatusOK, "unknown", nil},
		{"routing.retry.request-retry", `{"a":1}`, http.StatusOK, "unknown", nil},
	} {
		t.Run(tc.path+"/"+tc.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v8/management/config/"+strings.ReplaceAll(tc.path, ".", "/") {
					t.Error("escaped scoped endpoint", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer management-test-only" {
					t.Error("management credential missing")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			tr := &Transport{client: server.Client(), endpoint: server.URL}
			presence, value, err := tr.ReadConfigFieldValue(t.Context(), "management-test-only", tc.path)
			if tc.presence == "unknown" {
				if err == nil {
					t.Fatal("unsafe scalar accepted")
				}
				return
			}
			if err != nil || presence != tc.presence || value != tc.value {
				t.Fatal(presence, value, err)
			}
		})
	}
}
