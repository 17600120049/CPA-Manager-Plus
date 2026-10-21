package cpa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRetryAdapterScopedPresenceAndNoReplay(t *testing.T) {
	for _, body := range []string{"missing", "null", "0", "10", "1.5", "\"0\"", "{}"} {
		t.Run(body, func(t *testing.T) {
			var puts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != retryPath || r.Header.Get("Authorization") != "Bearer management-test-only" {
					t.Error("escaped scoped endpoint")
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
				if body == "missing" {
					w.WriteHeader(404)
				} else {
					w.Write([]byte(body))
				}
			}))
			defer server.Close()
			v := configObservation{SourceID: "isolated-source-1234", Generation: "7", Revision: 2, SHA256: strings.Repeat("a", 64), State: "persisted", Installed: true, Operation: configOperation{Scope: "initial-key"}}
			tr := &Transport{client: server.Client(), endpoint: server.URL, configurationAuthority: &privateConfigClient{
				grant: func() (configGrant, error) { return configGrant{SourceID: v.SourceID, Generation: v.Generation}, nil },
				call: func(_ context.Context, q configRequest) (configObservation, error) {
					switch q.Action {
					case "begin":
						v.Revision++
						v.State = "unknown"
						v.Operation = configOperation{ID: q.OperationID, Scope: q.Scope, Retry: q.Retry, State: "unknown"}
						out := v
						out.Created = true
						return out, nil
					case "commit":
						if q.ExpectedSHA256 != v.SHA256 {
							t.Fatal("not raw Runtime digest")
						}
						v.State = "persisted"
						v.Operation.State = "persisted"
					case "observe":
					default:
						t.Fatal("unexpected Runtime mutation", q.Action)
					}
					return v, nil
				},
			}}
			source, presence, value, e := tr.ReadRetry(t.Context(), "management-test-only")
			switch body {
			case "missing":
				if e != nil || presence != "missing" || value != nil {
					t.Fatal(presence, e)
				}
			case "null":
				if e != nil || presence != "null" || value != nil {
					t.Fatal(presence, e)
				}
			case "0", "10":
				if e != nil || presence != "present" || value == nil {
					t.Fatal(presence, e)
				}
			default:
				if e == nil {
					t.Fatal("unsafe scalar accepted")
				}
				return
			}
			if tr.BeginRetry(t.Context(), source, "retry-test", 0) != nil {
				t.Fatal("begin")
			}
			if e = tr.PutRetry(t.Context(), source, "retry-test", "management-test-only", 0); e != ErrApplyUnknown {
				t.Fatal("lost response not unknown", e)
			}
			for i := 0; i < 2; i++ {
				if _, e = tr.ReconcileRetry(t.Context(), source, "retry-test", 0); e != nil {
					t.Fatal(e)
				}
			}
			if puts.Load() != 1 {
				t.Fatal("reconciliation replayed PUT")
			}
		})
	}
}
func TestRetryAdapterRejectsDriftBeforePUT(t *testing.T) {
	for _, kind := range []string{"generation", "revision", "hash", "operation", "value", "scope", "grant"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("drift reached CPA") }))
			defer server.Close()
			src := RetrySource{SourceID: "isolated-source-1234", Generation: "7", Revision: 2, SHA256: strings.Repeat("a", 64)}
			v := configObservation{SourceID: src.SourceID, Generation: src.Generation, Revision: 3, SHA256: src.SHA256, State: "unknown", Operation: configOperation{ID: "retry-test", Scope: "request-retry", Retry: 0, State: "unknown"}}
			switch kind {
			case "generation":
				v.Generation = "8"
			case "revision":
				v.Revision++
			case "hash":
				v.SHA256 = strings.Repeat("b", 64)
			case "operation":
				v.Operation.ID = "foreign"
			case "value":
				v.Operation.Retry = 1
			case "scope":
				v.Operation.Scope = "initial-key"
			}
			tr := &Transport{client: server.Client(), endpoint: server.URL, configurationAuthority: &privateConfigClient{grant: func() (configGrant, error) {
				return configGrant{SourceID: src.SourceID, Generation: src.Generation}, nil
			}, call: func(context.Context, configRequest) (configObservation, error) {
				if kind == "grant" {
					return configObservation{}, ErrProofUnavailable
				}
				return v, nil
			}}}
			if e := tr.PutRetry(t.Context(), src, "retry-test", "management-test-only", 0); e == nil {
				t.Fatal("drift accepted")
			}
		})
	}
}
