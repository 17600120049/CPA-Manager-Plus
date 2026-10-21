package cpa

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKeyOperationSnapshotRejectsSourceDrift(t *testing.T) {
	for _, scenario := range []string{"valid", "extra key", "projection mismatch", "missing source", "remote management"} {
		t.Run(scenario, func(t *testing.T) {
			key := testSecret()
			root := strings.Replace(proofConfig, `"api-keys":[]`, `"api-keys":["`+key+`"]`, 1)
			if scenario == "remote management" {
				root = strings.Replace(root, `"allow-remote":false`, `"allow-remote":true`, 1)
			}
			if scenario == "extra key" {
				root = strings.Replace(root, `"api-keys":[`, `"unexpected":true,"api-keys":[`, 1)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+testManagement() {
					t.Error("wrong authority")
				}
				switch r.URL.Path {
				case "/v8/management/config":
					w.Write([]byte(root))
				case "/v8/management/config/access/api-keys":
					if scenario == "projection mismatch" {
						w.Write([]byte(`[]`))
					} else {
						json.NewEncoder(w).Encode([]string{key})
					}
				case "/v8/management/config.yaml":
					if scenario == "missing source" {
						w.WriteHeader(404)
					} else {
						w.Write([]byte("host-owned exact YAML bytes"))
					}
				default:
					t.Error("unexpected request")
				}
			}))
			defer server.Close()
			transport := &Transport{client: server.Client(), endpoint: server.URL}
			got, err := transport.KeySnapshot(t.Context(), testManagement())
			if scenario == "valid" {
				if err != nil || len(got.SourceSHA256) != 64 || len(got.Keys) != 1 {
					t.Fatal(got, err)
				}
			} else if err == nil {
				t.Fatal("unsafe source accepted")
			}
		})
	}
}

func TestKeyOperationPutNeverRetriesAndNeverClears(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "PUT" || r.URL.Path != "/v8/management/config/access/api-keys" {
			t.Error("wrong mutation")
		}
		w.WriteHeader(409)
	}))
	defer server.Close()
	transport := &Transport{client: server.Client(), endpoint: server.URL}
	for _, keys := range [][]string{nil, {}, {testSecret(), testSecret()}, {"invalid"}} {
		if !errors.Is(transport.WriteKeySet(t.Context(), testManagement(), keys), ErrSourceConflict) {
			t.Fatal("unsafe list accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid list reached CPA")
	}
	if !errors.Is(transport.WriteKeySet(t.Context(), testManagement(), []string{testSecret()}), ErrApplyUnknown) || calls != 1 {
		t.Fatal("409 retried or treated as rejection proof", calls)
	}
}
