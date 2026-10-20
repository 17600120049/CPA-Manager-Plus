package cpa

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const proofConfig = `{"config-version":8,"server":{"host":"127.0.0.1","port":8317},"management":{"allow-remote":false,"secret-key":"protected","disable-control-panel":true,"disable-auto-update-panel":true},"access":{"api-keys":[]},"oauth":{"auth-dir":"/runtime/gateway/auth"},"plugins":{"enabled":false,"dir":"/runtime/gateway/plugins"},"observability":{"logs":{"logging-to-file":false},"usage":{"usage-statistics-enabled":false}}}`

func goodProof() TargetProof {
	return TargetProof{Identity: privateRuntimeIdentity, Generation: 1, ArtifactID: privateCPAArtifact, Ready: true, Disposable: true, SourceExclusive: true, ExpiresAtMS: time.Now().Add(time.Minute).UnixMilli()}
}
func testSecret() string     { return "cpamp_sk_" + strings.Repeat("a", 64) }
func testManagement() string { return strings.Repeat("m", 32) }

func TestInstallOneIsOnlyInitialInstallAndCannotClaimEffective(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+testManagement() {
			t.Error("unexpected management credentials")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v8/management/config":
			w.Write([]byte(proofConfig))
		case "/v8/management/config/access/api-keys":
			if r.Method == http.MethodGet {
				if keys == nil {
					w.Write([]byte(`[]`))
				} else {
					json.NewEncoder(w).Encode(keys)
				}
				return
			}
			if r.Method != http.MethodPut {
				w.WriteHeader(405)
				return
			}
			puts++
			if err := json.NewDecoder(r.Body).Decode(&keys); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	writer := &Transport{client: server.Client(), endpoint: server.URL}
	out, err := writer.InstallOne(context.Background(), goodProof(), testManagement(), testSecret())
	if err != nil || !out.CPAConfigured || out.GatewayAuthorized || out.RuntimeState != "effect_unknown" {
		t.Fatalf("unexpected result: %#v %v", out, err)
	}
	_, err = writer.InstallOne(context.Background(), goodProof(), testManagement(), testSecret())
	if !errors.Is(err, ErrApplyUnknown) || puts != 1 {
		t.Fatalf("duplicate write not rejected: %v puts=%d", err, puts)
	}
}

func TestInstallOneRejectsUnsafeProofAndDirtyConfig(t *testing.T) {
	cases := []struct {
		name   string
		change func(*TargetProof)
		key    string
	}{
		{"wrong identity", func(p *TargetProof) { p.Identity = "other" }, testSecret()},
		{"wrong digest", func(p *TargetProof) { p.ArtifactID = "sha256:bad" }, testSecret()},
		{"missing generation", func(p *TargetProof) { p.Generation = 0 }, testSecret()},
		{"not isolated", func(p *TargetProof) { p.SourceExclusive = false }, testSecret()},
		{"not disposable", func(p *TargetProof) { p.Disposable = false }, testSecret()},
		{"offline", func(p *TargetProof) { p.Ready = false }, testSecret()},
		{"expired", func(p *TargetProof) { p.ExpiresAtMS = time.Now().Add(-time.Second).UnixMilli() }, testSecret()},
		{"short secret", func(p *TargetProof) {}, "cpamp_sk_foo"},
		{"uppercase hex", func(p *TargetProof) {}, "cpamp_sk_" + strings.Repeat("A", 64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := goodProof()
			tc.change(&p)
			result, err := NewTransport().InstallOne(context.Background(), p, testManagement(), tc.key)
			if !errors.Is(err, ErrProofUnavailable) || result.CPAConfigured || result.GatewayAuthorized {
				t.Fatalf("should stop: %#v %v", result, err)
			}
		})
	}
	if emptyProofConfig([]byte(strings.Replace(proofConfig, `"api-keys":[]`, `"api-keys":["other"]`, 1))) {
		t.Fatal("nonempty config permitted")
	}
	if emptyProofConfig([]byte(strings.Replace(proofConfig, `"127.0.0.1"`, `"0.0.0.0"`, 1))) {
		t.Fatal("broad bind permitted")
	}
	if emptyProofConfig([]byte(strings.Replace(proofConfig, `"config-version":8`, `"config-version":7`, 1))) {
		t.Fatal("wrong version permitted")
	}
	if emptyProofConfig([]byte(strings.Replace(proofConfig, `"plugins":{"enabled":false`, `"plugins":{"enabled":true`, 1))) {
		t.Fatal("plugin permitted")
	}
	if emptyProofConfig([]byte(strings.TrimSuffix(proofConfig, "}") + `,"providers":{}}`)) {
		t.Fatal("unrecognized provider permitted")
	}
}

func TestInstallOneUncertainWriteDoesNotRetryOrClaimAuth(t *testing.T) {
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v8/management/config":
			w.Write([]byte(proofConfig))
		case "/v8/management/config/access/api-keys":
			if r.Method == http.MethodPut {
				puts++
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	writer := &Transport{client: server.Client(), endpoint: server.URL}
	out, err := writer.InstallOne(context.Background(), goodProof(), testManagement(), testSecret())
	if !errors.Is(err, ErrApplyUnknown) || puts != 1 || out.GatewayAuthorized ||
		out.CPAConfigured || out.RuntimeState != "effect_unknown" {
		t.Fatalf("unsafe retry/result: %#v %v puts=%d", out, err, puts)
	}
	_, err = writer.InstallOne(context.Background(), goodProof(), testManagement(), testSecret())
	if !errors.Is(err, ErrApplyUnknown) || puts != 1 {
		t.Fatal("retried an unknown write", err, puts)
	}
}

func TestInitialSeedRejectsNullSafetyFieldsAndUnexpectedProviders(t *testing.T) {
	for _, field := range []string{`"allow-remote":false`, `"disable-control-panel":true`, `"enabled":false`, `"logging-to-file":false`, `"usage-statistics-enabled":false`} {
		split := strings.Index(field, ":")
		if emptyProofConfig([]byte(strings.Replace(proofConfig, field, field[:split+1]+"null", 1))) {
			t.Fatal("accepted null guard", field)
		}
	}
	withProvider := strings.TrimSuffix(proofConfig, "}") + `,"api-keys":` + privateSyntheticProvider + `}`
	if !emptyProofConfig([]byte(withProvider)) {
		t.Fatal("fixed synthetic upstream rejected")
	}
	if emptyProofConfig([]byte(strings.Replace(withProvider, "127.0.0.1:18321", "example.com", 1))) {
		t.Fatal("arbitrary upstream accepted")
	}
}
