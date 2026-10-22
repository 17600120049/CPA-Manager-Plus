package config

import "testing"

func TestPAHS0BuildIdentityCannotBeForgedByEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, runtime, compiled string
		wantOK bool
	}{
		{"normal", "", "0", true},
		{"matched-private", "1", "1", true},
		{"normal-image-with-forged-opt-in", "1", "0", false},
		{"private-image-disabled", "0", "1", false},
		{"missing-private-opt-in", "", "1", false},
		{"invalid-runtime", "maybe", "0", false},
		{"invalid-compiled", "1", "maybe", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePAHS0Build(tc.runtime, tc.compiled)
			if (err == nil) != tc.wantOK {
				t.Fatalf("runtime=%q compiled=%q err=%v", tc.runtime, tc.compiled, err)
			}
		})
	}
	// This test executes the actual default Go artifact: no runtime flag,
	// including the spoofed legacy image-marker variable, can activate P-S0.
	t.Setenv("CPAMP_PRIVATE_PAH_S0", "1")
	t.Setenv("CPAMP_PAH_S0_BUILD", "1")
	if _, err := LoadWithOptions(LoadOptions{}); err == nil {
		t.Fatal("forged environment activated ordinary Manager binary")
	}
}
