package cpa

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
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

// fieldAuthority models the Runtime private-configuration journal for one field:
// one reservation slot, one commit and the observation the Manager re-reads.
func fieldAuthority(v *configObservation, requestID, path string, value any) *privateConfigClient {
	return &privateConfigClient{
		grant: func() (configGrant, error) { return configGrant{SourceID: v.SourceID, Generation: v.Generation}, nil },
		call: func(_ context.Context, q configRequest) (configObservation, error) {
			switch q.Action {
			case "begin":
				if q.OperationID != requestID || q.Scope != path || q.Path != path ||
					!reflect.DeepEqual(NormalizeConfigValue(q.Value), value) {
					return configObservation{}, ErrSourceConflict
				}
				v.Revision++
				v.State = "unknown"
				v.Operation = configOperation{ID: q.OperationID, Scope: q.Scope, Path: q.Path, Value: q.Value, State: "unknown"}
				out := *v
				out.Created = true
				return out, nil
			case "commit":
				if q.ExpectedSHA256 != v.SHA256 || q.OperationID != requestID {
					return configObservation{}, ErrSourceConflict
				}
				v.State = "persisted"
				v.Operation.State = "persisted"
			case "observe":
			default:
				return configObservation{}, ErrSourceConflict
			}
			return *v, nil
		},
	}
}

// TestConfigFieldAdapterScopedPresenceAndNoReplay is the private H1/direct write
// unit: one scoped scalar read that keeps missing / null / present apart, then at
// most one PUT that is never repeated by reconciliation.
func TestConfigFieldAdapterScopedPresenceAndNoReplay(t *testing.T) {
	requestID := strings.Repeat("ab", 16)
	for _, tc := range []struct {
		path  string
		body  string
		want  string
		value any
	}{
		{"routing.retry.request-retry", "missing", "missing", nil},
		{"routing.retry.request-retry", "null", "null", nil},
		{"routing.retry.request-retry", "0", "present", 0},
		{"routing.retry.request-retry", "10", "present", 10},
		{"requests.passthrough-headers", "true", "present", true},
		{"requests.passthrough-headers", "false", "present", false},
		// A value the leaf's shape cannot carry is refused before any write. An
		// out-of-range integer is NOT a read error: the page must show what the
		// file actually holds, and the write path refuses it (see the reservation
		// test below).
		{"routing.retry.request-retry", "11", "present", 11},
		{"requests.streaming.keepalive-seconds", "301", "present", 301},
		{"routing.retry.request-retry", "1.5", "", nil},
		{"routing.retry.request-retry", `"0"`, "", nil},
		{"requests.passthrough-headers", "1", "", nil},
		{"routing.retry.request-retry", "{}", "", nil},
	} {
		t.Run(tc.path+"/"+tc.body, func(t *testing.T) {
			var puts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := "/v8/management/config/" + strings.ReplaceAll(tc.path, ".", "/")
				if r.URL.Path != want || r.Header.Get("Authorization") != "Bearer management-test-only" {
					t.Error("escaped scoped endpoint", r.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if r.Method == http.MethodPut {
					puts.Add(1)
					// The reply is lost: the write may have landed.
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				if tc.body == "missing" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			v := configObservation{SourceID: "isolated-source-1234", Generation: "7", Revision: 2, SHA256: strings.Repeat("a", 64), State: "persisted", Installed: true, Operation: configOperation{Scope: "initial-key"}}
			tr := &Transport{client: server.Client(), endpoint: server.URL, configurationAuthority: fieldAuthority(&v, requestID, tc.path, tc.value)}
			source, presence, value, err := tr.ReadConfigField(t.Context(), "management-test-only", tc.path)
			if tc.want == "" {
				if err == nil {
					t.Fatal("unsafe scalar accepted")
				}
				return
			}
			if err != nil || presence != tc.want || !reflect.DeepEqual(value, tc.value) {
				t.Fatalf("presence=%q value=%#v err=%v", presence, value, err)
			}
			if tc.value == nil || cpaOutOfHostRange(tc.path, tc.value) {
				// A presence-only read, or a value the Host would refuse: neither
				// reaches a write from here.
				return
			}
			if err = tr.BeginConfigField(t.Context(), source, requestID, tc.path, tc.value); err != nil {
				t.Fatalf("begin: %v", err)
			}
			if err = tr.PutConfigField(t.Context(), source, requestID, "management-test-only", tc.path, tc.value); err != ErrApplyUnknown {
				t.Fatalf("lost response not unknown: %v", err)
			}
			for i := 0; i < 2; i++ {
				if _, err = tr.ReconcileConfigField(t.Context(), source, requestID, tc.path, tc.value); err != nil {
					t.Fatal(err)
				}
			}
			if puts.Load() != 1 {
				t.Fatalf("reconciliation replayed the PUT: %d", puts.Load())
			}
		})
	}
}

// cpaOutOfHostRange reports a value the private Host bounds exclude, so the read
// test does not expect it to be writable.
func cpaOutOfHostRange(path string, value any) bool {
	return ValidatePrivateConfigLeafValue(path, value) != nil
}

// TestConfigFieldAdapterRejectsDriftBeforePUT proves the single PUT is refused
// when the Runtime source moved at all between the reservation and the write.
func TestConfigFieldAdapterRejectsDriftBeforePUT(t *testing.T) {
	requestID := strings.Repeat("ab", 16)
	path := "routing.retry.request-retry"
	for _, kind := range []string{"generation", "revision", "hash", "operation", "operationPath", "value", "state", "grant"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("drift reached CPA")
			}))
			defer server.Close()
			source := ConfigFieldSource{SourceID: "isolated-source-1234", Generation: "7", Revision: 2, SHA256: strings.Repeat("a", 64)}
			v := configObservation{SourceID: source.SourceID, Generation: source.Generation, Revision: 3, SHA256: source.SHA256, State: "unknown",
				Operation: configOperation{ID: requestID, Scope: path, Path: path, Value: 0, State: "unknown"}}
			switch kind {
			case "generation":
				v.Generation = "8"
			case "revision":
				v.Revision++
			case "hash":
				v.SHA256 = strings.Repeat("b", 64)
			case "operation":
				v.Operation.ID = strings.Repeat("cd", 16)
			case "operationPath":
				v.Operation.Path = "requests.passthrough-headers"
			case "value":
				v.Operation.Value = 1
			case "state":
				v.Operation.State = "persisted"
				v.State = "persisted"
			}
			tr := &Transport{client: server.Client(), endpoint: server.URL, configurationAuthority: &privateConfigClient{
				grant: func() (configGrant, error) {
					return configGrant{SourceID: source.SourceID, Generation: source.Generation}, nil
				},
				call: func(context.Context, configRequest) (configObservation, error) {
					if kind == "grant" {
						return configObservation{}, ErrProofUnavailable
					}
					return v, nil
				},
			}}
			if err := tr.PutConfigField(t.Context(), source, requestID, "management-test-only", path, 0); err == nil {
				t.Fatal("drift accepted")
			}
		})
	}
}

// TestConfigFieldBeginRequiresMatchingReservation proves the reservation is the
// only way in: a path outside the private subset, a wrong scalar, a foreign
// source or a Runtime refusal never reaches a write.
func TestConfigFieldBeginRequiresMatchingReservation(t *testing.T) {
	requestID := strings.Repeat("ab", 16)
	path := "requests.passthrough-headers"
	v := configObservation{SourceID: "isolated-source-1234", Generation: "7", Revision: 2, SHA256: strings.Repeat("a", 64), State: "persisted", Installed: true}
	authority := fieldAuthority(&v, requestID, path, true)
	source := ConfigFieldSource{SourceID: v.SourceID, Generation: v.Generation, Revision: v.Revision, SHA256: v.SHA256}
	tr := &Transport{configurationAuthority: authority}
	// A page leaf outside the frozen private subset is not a Host operation.
	if err := tr.BeginConfigField(t.Context(), source, requestID, "requests.proxy-url", "direct"); err != ErrProofUnavailable {
		t.Fatalf("non-private field reserved: %v", err)
	}
	if err := tr.BeginConfigField(t.Context(), source, requestID, path, "true"); err != ErrProofUnavailable {
		t.Fatalf("wrong scalar type reserved: %v", err)
	}
	// The Host bounds are enforced on the private subset, and only there.
	for _, out := range []struct {
		path  string
		value int
	}{
		{"routing.retry.request-retry", 11},
		{"routing.retry.request-retry", -1},
		{"requests.streaming.keepalive-seconds", 301},
		{"requests.streaming.bootstrap-retries", 11},
		{"requests.nonstream-keepalive-interval", 301},
		{"routing.retry.max-retry-interval", 301},
	} {
		if err := tr.BeginConfigField(t.Context(), source, requestID, out.path, out.value); err != ErrProofUnavailable {
			t.Fatalf("out-of-range %s=%d reserved: %v", out.path, out.value, err)
		}
	}
	// A leaf CPA validates itself keeps its shape contract only: CPAMP never
	// restates a CPA bound.
	if err := ValidateConfigLeafValue("requests.streaming.keepalive-seconds", 301); err != nil {
		t.Fatalf("CPA-backed leaf inherited the Host bound: %v", err)
	}
	if err := ValidatePrivateConfigLeafValue("requests.streaming.keepalive-seconds", 301); !errors.Is(err, ErrPrivateConfigOutOfRange) {
		t.Fatalf("private leaf accepted 301: %v", err)
	}
	if err := tr.BeginConfigField(t.Context(), ConfigFieldSource{SourceID: "other-source-1234", Generation: v.Generation, Revision: v.Revision}, requestID, path, true); err != ErrSourceConflict {
		t.Fatalf("foreign source reserved: %v", err)
	}
	if err := tr.BeginConfigField(t.Context(), source, requestID, path, false); err != ErrApplyUnknown {
		t.Fatalf("authority rejection mapped to a definite result: %v", err)
	}
	if err := tr.BeginConfigField(t.Context(), source, requestID, path, true); err != nil {
		t.Fatal(err)
	}
}
