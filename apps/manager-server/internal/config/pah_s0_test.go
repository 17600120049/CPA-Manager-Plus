package config

import "testing"

func TestPAHS0RequiresPairedManagerWebBuild(t *testing.T) {
	t.Setenv("CPAMP_PRIVATE_PAH_S0", "1")
	t.Setenv("CPAMP_PAH_S0_BUILD", "0")
	if _, err := LoadWithOptions(LoadOptions{}); err == nil {
		t.Fatal("P-S0 Manager without matched build must not start")
	}
	t.Setenv("CPAMP_PRIVATE_PAH_S0", "0")
	t.Setenv("CPAMP_PAH_S0_BUILD", "1")
	if _, err := LoadWithOptions(LoadOptions{}); err == nil {
		t.Fatal("P-S0 Web image with disabled runtime guard must not start")
	}
	t.Setenv("CPAMP_PRIVATE_PAH_S0", "maybe")
	if _, err := LoadWithOptions(LoadOptions{}); err == nil {
		t.Fatal("invalid runtime flag accepted")
	}
}
