package freshkeyapply

import (
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

func TestPermitSeparatesS2AAndS2BHostSource(t *testing.T) {
	now := time.Now()
	p := Permit{RunID: strings.Repeat("r", 16), Generation: "7", ManagementKey: strings.Repeat("m", 64), IssuedAtMS: now.UnixMilli(), ExpiresAtMS: now.Add(time.Minute).UnixMilli()}
	t.Setenv("CPAMP_PRIVATE_S2A_CHAT", "1")
	t.Setenv("CPAMP_PRIVATE_S2B_CHAT", "")
	if !p.valid(now) {
		t.Fatal("S2-A permit regression")
	}
	p.S2B = cpa.PrivateProviderSeed{Provider: "unit-provider", Model: "unit-model", Target: "https://provider.invalid/v1", CredentialSHA256: strings.Repeat("a", 64)}
	if p.valid(now) {
		t.Fatal("S2-A opt-in accepted real provider source")
	}
	t.Setenv("CPAMP_PRIVATE_S2B_CHAT", "1")
	if p.valid(now) {
		t.Fatal("conflicting profiles admitted")
	}
	t.Setenv("CPAMP_PRIVATE_S2A_CHAT", "")
	if !p.valid(now) {
		t.Fatal("valid explicit S2-B permit denied")
	}
	p.S2B.Model = ""
	if p.valid(now) {
		t.Fatal("unknown model admitted")
	}
	p.S2B = cpa.PrivateProviderSeed{}
	if p.valid(now) {
		t.Fatal("S2-B fell back to blank or synthetic seed")
	}
}
