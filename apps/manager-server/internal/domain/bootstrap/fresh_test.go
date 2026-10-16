package bootstrap

import "testing"

func ownedFresh() FreshOperation {
	return FreshOperation{Version: FreshContractVersion, Identity: FreshIdentity{
		AdmissionID: "admission-a", InstallationID: "installation-a", Generation: "generation-a",
		DatabasePath: "/data/manager.sqlite", OperationID: "fresh-operation-a",
	}, Revision: 1, GatewayPending: true}
}

func TestFreshContinuationRequiresCurrentOwnership(t *testing.T) {
	op := ownedFresh()
	ownership := op.Ownership()
	for _, admin := range []AdminCredentialState{AdminCredentialUninitialized, AdminCredentialInitialized} {
		evidence := ClassificationEvidence{AdminCredential: admin, Session: SessionMissing, FreshOwnership: &ownership}
		if got, err := Classify(evidence); err != nil || got != Fresh {
			t.Fatalf("owned %s: %s %v", admin, got, err)
		}
		if err := (StatusSnapshot{Mode: Fresh, AdminCredential: admin, Session: SessionExpired, FreshOwnership: &ownership}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func(*FreshOwnership){
		func(o *FreshOwnership) { o.CurrentGeneration = "other-generation" },
		func(o *FreshOwnership) { o.CurrentInstallationID = "other-installation" },
		func(o *FreshOwnership) { o.CurrentOperationID = "other-operation" },
		func(o *FreshOwnership) { o.Revision = 0 },
	} {
		bad := ownership
		mutate(&bad)
		got, err := Classify(ClassificationEvidence{AdminCredential: AdminCredentialInitialized, Session: SessionAuthenticated, FreshOwnership: &bad})
		if err != nil || got != Recovery {
			t.Fatalf("mismatched ownership: %s %v", got, err)
		}
		if (StatusSnapshot{Mode: Fresh, AdminCredential: AdminCredentialInitialized, Session: SessionAuthenticated, FreshOwnership: &bad}).Validate() == nil {
			t.Fatal("invalid status accepted")
		}
	}
	if got, err := Classify(ClassificationEvidence{AdminCredential: AdminCredentialInitialized, Session: SessionAuthenticated}); err != nil || got != Recovery {
		t.Fatalf("admin alone created Fresh: %s %v", got, err)
	}
}

func TestFreshCheckpointRejectsForeignRouteOwnerAndFalseCompletion(t *testing.T) {
	op := ownedFresh()
	if err := op.Validate(); err != nil {
		t.Fatal(err)
	}
	op.Routing.OperationID = "foreign-prefix-operation"
	if op.Validate() == nil {
		t.Fatal("foreign routing owner accepted")
	}
	op.Routing.OperationID = op.Identity.OperationID
	op.ProductComplete = true
	if op.Validate() == nil {
		t.Fatal("product completed without routing/strategy")
	}
	if CanActivatePublicGateway(GatewayActivationPreconditions{ProductInitialized: true}) {
		t.Fatal("product completion activated Gateway")
	}
}

func TestFreshRouteCheckpointRequiresOwnedCompleteShape(t *testing.T) {
	op := ownedFresh()
	valid := FreshRoutingCheckpoint{OperationID: op.Identity.OperationID, Revision: 1, Target: "/control", Stage: PrefixIntentPersisted}
	op.Routing = valid
	if err := op.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*FreshRoutingCheckpoint){
		func(c *FreshRoutingCheckpoint) { c.OperationID = "" },
		func(c *FreshRoutingCheckpoint) { c.Revision = 0 },
		func(c *FreshRoutingCheckpoint) { c.Target = "" },
		func(c *FreshRoutingCheckpoint) { c.Stage = "unknown" },
		func(c *FreshRoutingCheckpoint) { c.Stage = PrefixCommitted },
		func(c *FreshRoutingCheckpoint) { c.Committed = true },
	} {
		op.Routing = valid
		mutate(&op.Routing)
		if op.Validate() == nil {
			t.Fatal("malformed route checkpoint accepted")
		}
	}
}
