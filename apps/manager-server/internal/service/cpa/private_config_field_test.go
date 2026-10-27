package cpa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fieldAuthority models the Runtime private-configuration journal for one field.
func fieldAuthority(v *configObservation, requestID, path string, value any, puts *atomic.Int32) *privateConfigClient {
	return &privateConfigClient{
		grant: func() (configGrant, error) { return configGrant{SourceID: v.SourceID, Generation: v.Generation}, nil },
		call: func(_ context.Context, q configRequest) (configObservation, error) {
			switch q.Action {
			case "begin":
				if q.OperationID != requestID || q.Scope != path || q.Path != path || q.Value != value {
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
		{"routing.retry.request-retry", "11", "", nil},
		{"requests.streaming.keepalive-seconds", "301", "", nil},
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
					w.WriteHeader(500)
					return
				}
				if r.Method == "PUT" {
					puts.Add(1)
					conn, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					conn.Close()
					return
				}
				if tc.body == "missing" {
					w.WriteHeader(404)
					return
				}
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			v := configObservation{SourceID: "isolated-source-1234", Generation: "7", Revision: 2, SHA256: strings.Repeat("a", 64), State: "persisted", Installed: true, Operation: configOperation{Scope: "initial-key"}}
			tr := &Transport{client: server.Client(), endpoint: server.URL, configurationAuthority: fieldAuthority(&v, requestID, tc.path, tc.value, &puts)}
			source, presence, value, e := tr.ReadConfigField(t.Context(), "management-test-only", tc.path)
			if tc.want == "" {
				if e == nil {
					t.Fatal("unsafe scalar accepted")
				}
				return
			}
			if e != nil || presence != tc.want || value != tc.value {
				t.Fatal(presence, value, e)
			}
			if tc.value == nil {
				// A write always carries a scalar; presence-only reads stop here.
				return
			}
			if e = tr.BeginConfigField(t.Context(), source, requestID, tc.path, tc.value); e != nil {
				t.Fatal("begin", e)
			}
			if e = tr.PutConfigField(t.Context(), source, requestID, "management-test-only", tc.path, tc.value); e != ErrApplyUnknown {
				t.Fatal("lost response not unknown", e)
			}
			for i := 0; i < 2; i++ {
				if _, e = tr.ReconcileConfigField(t.Context(), source, requestID, tc.path, tc.value); e != nil {
					t.Fatal(e)
				}
			}
			if puts.Load() != 1 {
				t.Fatal("reconciliation replayed PUT")
			}
		})
	}
}

func TestConfigFieldAdapterRejectsDriftBeforePUT(t *testing.T) {
	requestID := strings.Repeat("ab", 16)
	for _, kind := range []string{"generation", "revision", "hash", "operation", "path", "value", "state", "grant"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("drift reached CPA")
			}))
			defer server.Close()
			source := ConfigFieldSource{SourceID: "isolated-source-1234", Generation: "7", Revision: 2, SHA256: strings.Repeat("a", 64)}
			v := configObservation{SourceID: source.SourceID, Generation: source.Generation, Revision: 3, SHA256: source.SHA256, State: "unknown",
				Operation: configOperation{ID: requestID, Scope: "routing.retry.request-retry", Path: "routing.retry.request-retry", Value: 0, State: "unknown"}}
			switch kind {
			case "generation":
				v.Generation = "8"
			case "revision":
				v.Revision++
			case "hash":
				v.SHA256 = strings.Repeat("b", 64)
			case "operation":
				v.Operation.ID = "foreign"
			case "path":
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
			if e := tr.PutConfigField(t.Context(), source, requestID, "management-test-only", "routing.retry.request-retry", 0); e == nil {
				t.Fatal("drift accepted")
			}
		})
	}
}

func TestConfigFieldBeginRequiresMatchingReservation(t *testing.T) {
	requestID := strings.Repeat("ab", 16)
	v := configObservation{SourceID: "isolated-source-1234", Generation: "7", Revision: 2, SHA256: strings.Repeat("a", 64), State: "persisted", Installed: true}
	authority := fieldAuthority(&v, requestID, "requests.passthrough-headers", true, nil)
	source := ConfigFieldSource{SourceID: v.SourceID, Generation: v.Generation, Revision: v.Revision, SHA256: v.SHA256}
	tr := &Transport{configurationAuthority: authority}
	if e := tr.BeginConfigField(t.Context(), source, requestID, "requests.proxy-url", "direct"); e != ErrProofUnavailable {
		t.Fatal("non-allowlisted field reserved", e)
	}
	if e := tr.BeginConfigField(t.Context(), source, requestID, "requests.passthrough-headers", "true"); e != ErrProofUnavailable {
		t.Fatal("wrong scalar type reserved", e)
	}
	if e := tr.BeginConfigField(t.Context(), ConfigFieldSource{SourceID: "other-source-1234", Generation: v.Generation, Revision: v.Revision}, requestID, "requests.passthrough-headers", true); e != ErrSourceConflict {
		t.Fatal("foreign source reserved", e)
	}
	// A Runtime rejection during begin leaves the outcome unknown, never replayable.
	if e := tr.BeginConfigField(t.Context(), source, requestID, "requests.passthrough-headers", false); e != ErrApplyUnknown {
		t.Fatal("authority rejection mapped to a definite result", e)
	}
	if e := tr.BeginConfigField(t.Context(), source, requestID, "requests.passthrough-headers", true); e != nil {
		t.Fatal(e)
	}
}
