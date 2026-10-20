package cpa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Deliberately unusable upstream and fake credential; never a live fixture.
func providerFixture() (PrivateProviderSeed, string) {
	credential := "unit-test-credential-only"
	h := sha256.Sum256([]byte(credential))
	s := PrivateProviderSeed{Provider: "unit-provider", Model: "unit-model", Target: "https://provider.invalid/v1", CredentialSHA256: hex.EncodeToString(h[:])}
	p := `{"openai-compatibility":[{"name":"unit-provider","base-url":"https://provider.invalid/v1","request-retry":0,"keys":[{"api-key":"` + credential + `","auth_index":"1234567890abcdef"}],"models":[{"name":"unit-model","alias":"unit-model"}]}]}`
	return s, strings.TrimSuffix(proofConfig, "}") + `,"api-keys":` + p + `}`
}

func TestPrivateProviderSeedRejectsDriftWithoutWeakeningS1(t *testing.T) {
	s, config := providerFixture()
	if !privateProviderConfig([]byte(config), s, "") || emptyProofConfig([]byte(config)) {
		t.Fatal("profile separation failed")
	}
	for _, change := range []struct{ old, new string }{
		{`"unit-provider"`, `"unknown"`}, {`"unit-model"`, `"other-model"`},
		{`https://provider.invalid/v1`, `https://other.invalid/v1`},
		{`unit-test-credential-only`, `different-test-credential`},
		{`"request-retry":0`, `"request-retry":1`}, {`"request-retry":0`, `"request-retry":null`},
		{`"request-retry":0`, `"request-retry":0,"proxy-url":"http://other.invalid"`},
		{`"api-keys":[]`, `"api-keys":["staged"]`},
		{`"models":[`, `"models":[{"name":"other","alias":"other"},`},
		{`"keys":[`, `"keys":[{"api-key":"second-key"},`},
		{`"openai-compatibility":[`, `"unknown":[],"openai-compatibility":[`},
		{`"name":"unit-provider"`, `"name":"other","name":"unit-provider"`},
		{`"name":"unit-provider"`, `"Name":"other","name":"unit-provider"`},
		{`"server":`, `"SERVER":{},"server":`},
		{`"port":8317`, `"port":8317,"extra":false`},
		{`"1234567890abcdef"`, `"unknown"`},
	} {
		if privateProviderConfig([]byte(strings.Replace(config, change.old, change.new, 1)), s, "") {
			t.Fatal("unsafe provider change accepted", change.old)
		}
	}
	for _, target := range []string{"http://provider.invalid/v1", "https://127.0.0.1/v1", "https://provider.invalid:443/v1", "https://user@provider.invalid/v1", "https://provider.invalid/v1?", "https://provider.invalid/v1#x"} {
		other := s
		other.Target = target
		if other.Valid() {
			t.Fatal("invalid target admitted")
		}
	}
	for _, base := range []string{proofConfig, strings.TrimSuffix(proofConfig, "}") + `,"api-keys":` + privateSyntheticProvider + `}`} {
		if !emptyProofConfig([]byte(base)) || privateProviderConfig([]byte(base), s, "") {
			t.Fatal("S1/S2-A regression or S2-B fallback")
		}
	}
}

func TestS2BUsesOnlyExistingInstallWriterAndRechecksProvider(t *testing.T) {
	s, original := providerFixture()
	config := original
	var keys []string
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/management/config" {
			w.Write([]byte(config))
			return
		}
		if r.URL.Path != "/v8/management/config/access/api-keys" {
			w.WriteHeader(404)
			return
		}
		if r.Method == "PUT" {
			puts++
			json.NewDecoder(r.Body).Decode(&keys)
			b, _ := json.Marshal(keys)
			config = strings.Replace(original, `"api-keys":[]`, `"api-keys":`+string(b), 1)
		}
		if keys == nil {
			w.Write([]byte(`[]`))
		} else {
			json.NewEncoder(w).Encode(keys)
		}
	}))
	defer server.Close()
	sourceErr := error(nil)
	w := NewPrivateS2BTransport(func() (PrivateProviderSeed, error) { return s, sourceErr })
	w.client, w.endpoint = server.Client(), server.URL
	p := goodProof()
	p.ProviderSeed = s
	wrong := p
	wrong.ProviderSeed.Model = "different"
	if _, err := w.InstallOne(t.Context(), wrong, testManagement(), testSecret()); !errors.Is(err, ErrSourceConflict) || puts != 0 {
		t.Fatal("unbound source reached writer")
	}
	out, err := w.InstallOne(t.Context(), p, testManagement(), testSecret())
	if err != nil || !out.CPAConfigured || out.GatewayAuthorized || puts != 1 {
		t.Fatal("one-shot install failed", err)
	}
	if _, err = w.InstallOne(t.Context(), p, testManagement(), testSecret()); !errors.Is(err, ErrApplyUnknown) || puts != 1 {
		t.Fatal("second writer attempt")
	}
	if ok, err := w.Configured(t.Context(), testManagement(), testSecret()); !ok || err != nil {
		t.Fatal("configured provider rejected", err)
	}
	config = strings.Replace(config, "https://provider.invalid/v1", "https://other.invalid/v1", 1)
	if ok, _ := w.Configured(t.Context(), testManagement(), testSecret()); ok {
		t.Fatal("drifted provider admitted")
	}
	config = strings.Replace(original, `"api-keys":[]`, `"api-keys":["`+testSecret()+`"]`, 1)
	sourceErr = context.Canceled
	if ok, _ := w.Configured(t.Context(), testManagement(), testSecret()); ok {
		t.Fatal("missing trusted source admitted")
	}
}

func TestPrivateProfilesDoNotFallbackAcrossSeedAuthorities(t *testing.T) {
	seed, realStyle := providerFixture()
	for _, scenario := range []string{"missing-host-source", "S1-with-S2B-seed"} {
		t.Run(scenario, func(t *testing.T) {
			puts := 0
			config := proofConfig
			writer := NewPrivateS2BTransport(nil)
			p := goodProof()
			if scenario == "S1-with-S2B-seed" {
				config = realStyle
				writer = NewTransport()
				p.ProviderSeed = seed
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" {
					puts++
				}
				if r.URL.Path == "/v8/management/config" {
					w.Write([]byte(config))
				} else {
					w.Write([]byte(`[]`))
				}
			}))
			defer server.Close()
			writer.client, writer.endpoint = server.Client(), server.URL
			if _, err := writer.InstallOne(t.Context(), p, testManagement(), testSecret()); !errors.Is(err, ErrSourceConflict) || puts != 0 {
				t.Fatal("untrusted seed reached writer", err, puts)
			}
		})
	}
}
