package freshconfig

import (
	"context"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
)

// The page never decides for itself whether a checkpoint belongs to this
// installation: it asks the installation authority the key surface asks — the
// same function, bound to this page's surface — so a default install whose
// managed destination the authority owns is writable and anything else fails
// closed on the page as well.
func TestGatewayConfigPageAsksTheInstallationAuthority(t *testing.T) {
	wire := &fakeWire{document: map[string]any{
		"routing":  map[string]any{"retry": map[string]any{"request-retry": 3}},
		"requests": map[string]any{"streaming": map[string]any{"keepalive-seconds": 15}},
	}}
	store := &memoryStore{record: managedRecord()}
	target := func(context.Context, uint64) (string, error) { return "management-test-only", nil }

	refused := false
	asked := 0
	service := New(store, target, func(bootstrap.FreshOperation) bool { asked++; return !refused })
	service.wire = wire
	service.transport = nil
	service.managed = true
	service.endpoint = "http://127.0.0.1:8317"

	view, err := service.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if asked == 0 {
		t.Fatal("the page did not ask the installation authority")
	}
	if view.Capability.Mode != ModeManaged || !view.Capability.Writable {
		t.Fatalf("owned checkpoint capability %+v", view.Capability)
	}

	refused = true
	view, err = service.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.Capability.Mode != ModeUnavailable || view.Capability.Readable || view.Capability.Writable ||
		view.Capability.Reason != ReasonNotInstalled {
		t.Fatalf("refused checkpoint capability %+v", view.Capability)
	}

	// A service without any installation authority never becomes a write path.
	anonymous := New(store, target, nil)
	anonymous.wire = wire
	anonymous.transport = nil
	anonymous.managed = true
	anonymous.endpoint = "http://127.0.0.1:8317"
	if view, err = anonymous.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	if view.Capability.Mode != ModeUnavailable || view.Capability.Writable {
		t.Fatalf("anonymous authority capability %+v", view.Capability)
	}
}

// The private H1/direct page keeps its own scope, but it now takes the same
// answer as the key surface and stays read-only when the authority refuses.
func TestPrivateGatewayPageAsksTheInstallationAuthority(t *testing.T) {
	service, _, _, _ := privateFixture(t, false)
	service.targets = func(bootstrap.FreshOperation) bool { return false }
	view, err := service.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.Capability.Mode != ModePrivate || view.Capability.Readable || view.Capability.Writable ||
		view.Capability.Reason != ReasonNotInstalled {
		t.Fatalf("private capability %+v", view.Capability)
	}
}
