package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestPAHS0ConfigRejectsInvalidOrUnsharedTopology(t *testing.T) {
	base := map[string]string{
		"CPAMP_PRIVATE_PAH_S0": "1",
		"CPAMP_MANAGER_URL": "http://127.0.0.1:18319",
		"CPAMP_GATEWAY_URL": "http://127.0.0.1:8317",
		"CPAMP_INGRESS_CONTROL_ADDR": "127.0.0.1:9082",
	}
	get := func(k string) string { return base[k] }
	cfg, err := loadConfig(get)
	if err != nil || !cfg.pahS0 {
		t.Fatalf("private profile rejected: %+v %v", cfg, err)
	}
	for _, tc := range []struct{key, value string}{
		{"CPAMP_MANAGER_URL", "http://cpamp-manager:18319"},
		{"CPAMP_GATEWAY_URL", "http://cpamp-runtime:8317"},
		{"CPAMP_INGRESS_CONTROL_ADDR", "0.0.0.0:9082"},
		{"CPAMP_PRIVATE_PAH_S0", "unexpected"},
	} {
		original := base[tc.key]
		base[tc.key] = tc.value
		if _, err := loadConfig(get); err == nil {
			t.Fatalf("%s=%q should fail private admission", tc.key, tc.value)
		}
		base[tc.key] = original
	}
}

func TestPAHS0ManagerProbeRequiresNoStore204(t *testing.T) {
	status := http.StatusNoContent
	cache := "no-store"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/pah-s0/resource-guard" {
			t.Errorf("unexpected probe path %s", r.URL.Path)
		}
		w.Header().Set("Cache-Control", cache)
		w.WriteHeader(status)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	probe := managerPAHS0Guard(u)
	if !probe(t.Context()) {
		t.Fatal("matching Manager opt-in rejected")
	}
	cache = ""
	if probe(t.Context()) {
		t.Fatal("no-store missing")
	}
	cache, status = "no-store", http.StatusFound
	if probe(t.Context()) {
		t.Fatal("redirect opened guard")
	}
}
