package cpa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrivateConfigurationRequiresCompleteCommittedFile(t *testing.T) {
	raw := []byte("access: {api-keys: [private-key]}\nmanagement: {secret-key: protected}\n")
	zero := 0
	for _, tc := range []struct {
		name  string
		state string
		hash  string
		retry *int
		body  string
		want  bool
	}{
		{"committed recovered snapshot", "persisted", hashBytes(raw), &zero, `{"access":{},"routing":{"retry":{"request-retry":0}}}`, true},
		{"wrong leaf", "persisted", hashBytes(raw), &zero, `{"routing":{"retry":{"request-retry":1}}}`, false},
		{"uncommitted leaf", "persisted", hashBytes(raw), nil, `{"routing":{"retry":{"request-retry":0}}}`, false},
		{"additional routing field", "persisted", hashBytes(raw), &zero, `{"routing":{"retry":{"request-retry":0},"extra":false}}`, false},
		{"unknown", "unknown", hashBytes(raw), &zero, `{}`, false},
		{"Runtime reports full-file drift", "unknown", strings.Repeat("0", 64), &zero, `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v8/management/config.yaml" {
					t.Error("unexpected request")
					w.WriteHeader(500)
					return
				}
				w.Write(raw)
			}))
			defer server.Close()
			tr := &Transport{client: server.Client(), endpoint: server.URL, configurationAuthority: &privateConfigClient{
				grant: func() (configGrant, error) { return configGrant{SourceID: "test-source", Generation: "7"}, nil },
				call: func(_ context.Context, q configRequest) (configObservation, error) {
					if q.Action != "observe" {
						t.Fatal("unexpected mutation")
					}
					return configObservation{State: tc.state, Installed: true, SHA256: tc.hash, CommittedRetry: tc.retry,
						Operation: configOperation{State: "recovered", Scope: "request-retry", Retry: 1}}, nil
				},
			}}
			got, e := tr.postInstallProjection(context.Background(), []byte(tc.body))
			if (e == nil) != tc.want {
				t.Fatal("projection acceptance", e)
			}
			if e == nil {
				var root map[string]any
				json.Unmarshal(got, &root)
				if _, ok := root["routing"]; ok {
					t.Fatal("projection retained permitted retry")
				}
			}
		})
	}
}

func TestOriginalInitialOperationReconcilesWithoutAnotherPUT(t *testing.T) {
	secret := testSecret()
	raw := []byte("complete preserved YAML")
	persisted := false
	commits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("reconciliation repeated mutation")
			w.WriteHeader(500)
			return
		}
		switch r.URL.Path {
		case "/v8/management/config.yaml":
			w.Write(raw)
		case "/v8/management/config/access/api-keys":
			json.NewEncoder(w).Encode([]string{secret})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	tr := &Transport{client: server.Client(), endpoint: server.URL, configurationAuthority: &privateConfigClient{
		grant: func() (configGrant, error) { return configGrant{SourceID: "test-source", Generation: "7"}, nil },
		call: func(_ context.Context, q configRequest) (configObservation, error) {
			op := configOperation{ID: "s1-" + hashBytes([]byte(secret)), KeySHA256: hashBytes([]byte(secret)), Scope: "initial-key", State: "unknown"}
			if q.Action == "commit" {
				if q.OperationID != op.ID || q.ExpectedSHA256 != hashBytes(raw) {
					t.Fatal("unbound reconciliation")
				}
				commits++
				persisted = true
			} else if q.Action != "observe" {
				t.Fatal("replayed reservation")
			}
			state := "unknown"
			if persisted {
				state = "persisted"
				op.State = state
			}
			return configObservation{State: state, Installed: persisted, SHA256: hashBytes(raw), Revision: 2, Operation: op}, nil
		},
	}}
	for i := 0; i < 2; i++ {
		ok, e := tr.Configured(context.Background(), "management", secret)
		if !ok || e != nil {
			t.Fatal(ok, e)
		}
	}
	if commits != 1 {
		t.Fatal("repeated terminal recording", commits)
	}
}

func TestPrivateConfigurationTransportDefaultOff(t *testing.T) {
	t.Setenv("CPAMP_PRIVATE_CONFIG_AUTHORITY", "")
	if NewTransport().configurationAuthority != nil {
		t.Fatal("enabled without Host opt-in")
	}
	t.Setenv("CPAMP_PRIVATE_CONFIG_AUTHORITY", "invalid")
	tr := NewTransport()
	if _, _, e := tr.configuration(context.Background(), "observe"); e == nil {
		t.Fatal("invalid opt-in silently bypassed authority")
	}
}

func TestInitialPUTCannotOutlivePermitDuringDurableReservation(t *testing.T) {
	proof := goodProof()
	proof.ExpiresAtMS = time.Now().Add(2 * time.Second).UnixMilli()
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts++
			w.WriteHeader(200)
			return
		}
		if r.URL.Path == "/v8/management/config" {
			w.Write([]byte(proofConfig))
		} else {
			w.Write([]byte("[]"))
		}
	}))
	defer server.Close()
	tr := &Transport{client: server.Client(), endpoint: server.URL, configurationAuthority: &privateConfigClient{
		grant: func() (configGrant, error) { return configGrant{SourceID: "test-source", Generation: "1"}, nil },
		call: func(_ context.Context, q configRequest) (configObservation, error) {
			v := configObservation{State: "persisted", SHA256: strings.Repeat("a", 64), Revision: 1}
			if q.Action == "begin" {
				time.Sleep(time.Until(time.UnixMilli(proof.ExpiresAtMS).Add(time.Millisecond)))
				v.Created = true
				v.State = "unknown"
				v.Revision = 2
				v.Operation.ID = q.OperationID
			}
			return v, nil
		},
	}}
	_, e := tr.InstallOne(context.Background(), proof, testManagement(), testSecret())
	if e != ErrApplyUnknown || puts != 0 || !tr.attempted {
		t.Fatal("expired permit submitted PUT", e, puts)
	}
}
