package freshkeyapply

import (
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

// Targets is the one profile discriminator the key install, the key operation,
// the chat consumer and the gateway configuration page ask. A managed
// installation owns exactly its committed managed destination; the private
// profile owns exactly the attested private target it has always owned.
func TestTargetsSelectsTheProfileOfTheService(t *testing.T) {
	managedDestination := "managed:sha256:" + strings.Repeat("c", 64)
	managedCheckpoint := bootstrap.FreshStrategyCheckpoint{
		Strategy: bootstrap.NewManaged, Stage: "committed",
		TargetIdentity: managedRuntimeIdentity, TargetGeneration: 9, TargetArtifactID: managedArtifact(),
		DestinationConnectionIdentity: managedDestination,
	}
	privateCheckpoint := bootstrap.FreshStrategyCheckpoint{
		Strategy: bootstrap.NewManaged, Stage: "committed",
		TargetIdentity: cpa.PrivateKeyRuntime, TargetGeneration: 9, TargetArtifactID: cpa.PrivateKeyArtifact,
	}
	// The private H1/direct scope predates the managed checkpoint: its page and
	// field journal accept an adopted checkpoint naming the same attested target.
	adoptedPrivate := privateCheckpoint
	adoptedPrivate.Strategy = bootstrap.AdoptExisting
	externalCheckpoint := bootstrap.FreshStrategyCheckpoint{
		Strategy: bootstrap.ConnectExternal, Stage: "committed",
		TargetIdentity: "external:sha256:" + strings.Repeat("d", 64), TargetGeneration: 1,
	}
	stagedPrivate := privateCheckpoint
	stagedPrivate.Stage = "staged"
	wrongArtifact := privateCheckpoint
	wrongArtifact.TargetArtifactID = "sha256:" + strings.Repeat("e", 64)
	unboundManaged := managedCheckpoint
	unboundManaged.DestinationConnectionIdentity = ""
	stagedManaged := managedCheckpoint
	stagedManaged.Stage = "staged"

	private := New(nil, nil, nil)
	managed := NewManaged(nil, nil, nil, "http://127.0.0.1:8317", nil)
	if private.profile != ProfilePrivate {
		t.Fatalf("New profile = %q", private.profile)
	}
	if managed.profile != ProfileManaged {
		t.Fatalf("NewManaged profile = %q", managed.profile)
	}
	var nilService *Service
	for _, test := range []struct {
		name    string
		service *Service
		check   bootstrap.FreshStrategyCheckpoint
		want    bool
	}{
		{"nil", nilService, managedCheckpoint, false},
		{"managed owns the managed destination", managed, managedCheckpoint, true},
		{"managed refuses the private target", managed, privateCheckpoint, false},
		{"managed refuses an unbound checkpoint", managed, unboundManaged, false},
		{"managed refuses a staged checkpoint", managed, stagedManaged, false},
		{"managed refuses an external checkpoint", managed, externalCheckpoint, false},
		{"private owns the attested target", private, privateCheckpoint, true},
		{"private keeps its adopted target", private, adoptedPrivate, true},
		{"private refuses a staged target", private, stagedPrivate, false},
		{"private refuses a foreign artifact", private, wrongArtifact, false},
		{"private refuses the managed destination", private, managedCheckpoint, false},
		{"private refuses an external checkpoint", private, externalCheckpoint, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.service.Targets(bootstrap.FreshOperation{Strategy: test.check}); got != test.want {
				t.Fatalf("Targets = %v, want %v", got, test.want)
			}
		})
	}
}
