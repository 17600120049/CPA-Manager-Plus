package freshconfig

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

// privateWire is the private H1/direct transport: one reservation slot, one
// non-replayable PUT of one scalar, and the value the Host now holds. It embeds
// the group-path fake so the whole-document read and the group PATCH stay
// observable and a private save that wandered into the group path is visible.
type privateWire struct {
	*fakeWire
	source    cpa.ConfigFieldSource
	operation cpa.ConfigFieldSource
	value     any
	present   bool
	begins    int
	puts      int
	lostPut   bool
	h1        *privateH1
}

func (p *privateWire) observe() cpa.ConfigFieldSource {
	out := p.source
	out.OperationID = p.operation.OperationID
	out.OperationScope = p.operation.OperationScope
	out.OperationPath = p.operation.OperationPath
	out.OperationValue = p.operation.OperationValue
	out.OperationState = p.operation.OperationState
	return out
}

func (p *privateWire) ConfigFieldSource(context.Context) (cpa.ConfigFieldSource, error) {
	return p.observe(), nil
}

func (p *privateWire) ReadConfigField(_ context.Context, _ string, path string) (cpa.ConfigFieldSource, string, any, error) {
	source := p.observe()
	if !p.present {
		return source, "missing", nil, nil
	}
	return source, "present", p.value, nil
}

func (p *privateWire) BeginConfigField(_ context.Context, _ cpa.ConfigFieldSource, id, path string, value any) error {
	p.begins++
	if cpa.ValidatePrivateConfigLeafValue(path, value) != nil {
		return cpa.ErrProofUnavailable
	}
	p.operation = cpa.ConfigFieldSource{OperationID: id, OperationScope: path, OperationPath: path, OperationValue: value, OperationState: "unknown"}
	p.source.Revision++
	p.source.State = "unknown"
	return nil
}

func (p *privateWire) PutConfigField(_ context.Context, _ cpa.ConfigFieldSource, _ string, _ string, path string, value any) error {
	p.puts++
	if cpa.ValidatePrivateConfigLeafValue(path, value) != nil {
		return cpa.ErrApplyUnknown
	}
	p.value, p.present = value, true
	setDocumentValue(p.document, strings.Split(path, "."), value)
	if p.lostPut {
		return cpa.ErrApplyUnknown
	}
	return nil
}

func (p *privateWire) WriteH1ConfigField(_ context.Context, parent string, step cpa.H1Step, _ string, path string, value any) error {
	p.puts++
	if p.h1 == nil || parent != p.h1.parent.ID || step.Scope != path || step.ParentID != parent {
		return cpa.ErrProofUnavailable
	}
	if cpa.ValidatePrivateConfigLeafValue(path, value) != nil {
		return cpa.ErrProofUnavailable
	}
	p.value, p.present = value, true
	setDocumentValue(p.document, strings.Split(path, "."), value)
	// The released Host keeps the completed step as its last operation.
	p.operation = cpa.ConfigFieldSource{OperationID: step.ID, OperationScope: path, OperationPath: path, OperationValue: value, OperationState: "persisted"}
	p.source.Revision++
	p.source.State = "persisted"
	return nil
}

func (p *privateWire) ReconcileConfigField(_ context.Context, source cpa.ConfigFieldSource, requestID, path string, value any) (cpa.ConfigFieldSource, error) {
	out := p.observe()
	if out.OperationID != requestID || out.OperationPath != path || canonicalDigest(out.OperationValue) != canonicalDigest(value) {
		return out, cpa.ErrSourceConflict
	}
	p.source.State = "persisted"
	p.operation.OperationState = "persisted"
	return p.observe(), nil
}

// privateH1 is the Manager-side half of the Host handoff: one parent, its single
// step and the terminal release.
type privateH1 struct {
	revision func() uint64
	parent   cpa.H1Parent
	claimed  int
	finished int
}

func (h *privateH1) Claim(_ context.Context, _ *cpa.Transport, intent cpa.H1Intent) (*cpa.H1Parent, error) {
	h.claimed++
	h.parent = cpa.H1Parent{
		ID: cpa.H1ParentID(intent), Intent: intent, Generation: "3", SourceID: "install-identity-0001",
		ClaimSHA256: strings.Repeat("a", 64), ClaimRevision: h.revision() + 1, Phase: "held",
	}
	parent := h.parent
	return &parent, nil
}

func (h *privateH1) Begin(_ context.Context, _ *cpa.Transport, parent string, n int) (cpa.H1Step, error) {
	step := cpa.H1Step{
		ParentID: parent, SourceID: "install-identity-0001", Generation: "3",
		ID: cpa.H1StepID(h.parent.Intent, n), Scope: cpa.H1IntentStepScope(h.parent.Intent),
		State: "unknown", BeginRevision: h.revision() + 2,
	}
	h.parent.Steps = append(h.parent.Steps, step)
	return step, nil
}

func (h *privateH1) Commit(_ context.Context, _ *cpa.Transport, _ string, n int) (cpa.H1Step, error) {
	step := h.parent.Steps[n]
	step.State = "persisted"
	step.Receipt = strings.Repeat("e", 64)
	return step, nil
}

func (h *privateH1) ParentRecord(_ context.Context, _ *cpa.Transport, _ string) (*cpa.H1Parent, error) {
	parent := h.parent
	return &parent, nil
}

func (h *privateH1) Finish(_ context.Context, _ *cpa.Transport, _ string, _ string) error {
	h.finished++
	return nil
}

// privateFixture is a committed private H1/direct installation. Its record names
// the attested private runtime, which is the only identity the private write path
// accepts.
func privateFixture(t *testing.T, h1 bool) (*Service, *privateWire, *privateH1, *memoryStore) {
	t.Helper()
	group := &fakeWire{
		document: map[string]any{
			"routing":  map[string]any{"retry": map[string]any{"request-retry": 3, "max-retry-interval": 30}},
			"requests": map[string]any{"streaming": map[string]any{"keepalive-seconds": 15}},
		},
		runtime: true,
	}
	wire := &privateWire{
		fakeWire: group,
		source: cpa.ConfigFieldSource{
			SourceID: "install-identity-0001", Generation: "3", Revision: 2,
			SHA256: strings.Repeat("a", 64), State: "persisted", Installed: true,
			OperationScope: "initial-key",
		},
		value: 3, present: true,
	}
	store := &memoryStore{record: managedRecord()}
	store.record.Operation.Strategy = bootstrap.FreshStrategyCheckpoint{
		Strategy: bootstrap.AdoptExisting, Stage: "committed",
		TargetIdentity: cpa.PrivateKeyRuntime, TargetArtifactID: cpa.PrivateKeyArtifact, TargetGeneration: 3,
	}
	service := New(store, func(context.Context, uint64) (string, error) { return "management-test-only", nil }, privateTargets)
	service.wire = wire
	service.transport = nil
	if !h1 {
		return service, wire, nil, store
	}
	wire.source.H1 = true
	wire.source.HandoffValid = true
	wire.source.OperationScope = ""
	wire.source.OperationID = ""
	coordinator := &privateH1{revision: func() uint64 { return wire.source.Revision }}
	service.transport = cpa.NewTransport()
	service.h1 = coordinator
	wire.h1 = coordinator
	return service, wire, coordinator, store
}

// TestPrivateProfileWritesOnlyItsSixLeaves is the private contract: the page is
// readable, exactly the six shipped leaves are editable, and a save is exactly
// one "set" of one of them. The Host reservation protocol writes one field per
// operation and has no delete, so everything else is refused before any write.
func TestPrivateProfileWritesOnlyItsSixLeaves(t *testing.T) {
	service, wire, _, store := privateFixture(t, false)
	view, err := service.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.Capability.Mode != ModePrivate || !view.Capability.Readable || !view.Capability.Writable || view.Capability.RulesWritable {
		t.Fatalf("private capability %+v", view.Capability)
	}
	if view.Capability.Reason != ReasonPrivateFrozen {
		t.Fatalf("private reason %q", view.Capability.Reason)
	}
	// The write shape is stated, not inferred: one set edit, no "use default".
	if view.ProfileLimits == nil || !view.ProfileLimits.SingleEdit || view.ProfileLimits.UseDefault {
		t.Fatalf("private limits %+v", view.ProfileLimits)
	}
	editable := map[string]bool{}
	for _, field := range view.Fields {
		if field.Editable {
			editable[field.Path] = true
		}
	}
	if len(editable) != 6 {
		t.Fatalf("private page offered %d editable leaves: %v", len(editable), editable)
	}
	for _, path := range []string{
		"routing.retry.request-retry", "routing.retry.max-retry-interval",
		"requests.passthrough-headers", "requests.nonstream-keepalive-interval",
		"requests.streaming.keepalive-seconds", "requests.streaming.bootstrap-retries",
	} {
		if !editable[path] {
			t.Fatalf("shipped private leaf %s is not editable", path)
		}
	}
	for _, path := range []string{"routing.strategy", "routing.retry.max-retry-credentials", "requests.proxy-url"} {
		if editable[path] {
			t.Fatalf("leaf %s outside the private scope is editable", path)
		}
	}
	for _, group := range view.Groups {
		switch group.ID {
		case "retry", "request", "streaming":
			if !group.Writable {
				t.Fatalf("private group %s is not writable: %+v", group.ID, group)
			}
		default:
			t.Fatalf("private page exposed a group it cannot write: %+v", group)
		}
	}

	// A second edit has no Host instruction.
	if _, err = service.SaveGroup(context.Background(), "retry", GroupRequest{
		RequestID: strings.Repeat("a1", 16),
		Expected:  Expected{Generation: "3", Baseline: baseline(t, service, "retry")},
		Edits: []Edit{
			{Path: "routing.retry.request-retry", Action: "set", Value: json.RawMessage("5")},
			{Path: "routing.retry.max-retry-interval", Action: "set", Value: json.RawMessage("60")},
		},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("two-edit private save accepted: %v", err)
	}
	// "Use default" is a delete, and the H1 protocol has none.
	if _, err = service.SaveGroup(context.Background(), "retry", GroupRequest{
		RequestID: strings.Repeat("a2", 16),
		Expected:  Expected{Generation: "3", Baseline: baseline(t, service, "retry")},
		Edits:     []Edit{{Path: "routing.retry.max-retry-interval", Action: "default"}},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("private default edit accepted: %v", err)
	}
	// A private group's public leaf list is wider than the private subset.
	if _, err = service.SaveGroup(context.Background(), "retry", GroupRequest{
		RequestID: strings.Repeat("a3", 16),
		Expected:  Expected{Generation: "3", Baseline: baseline(t, service, "retry")},
		Edits:     []Edit{{Path: "routing.retry.max-retry-credentials", Action: "set", Value: json.RawMessage("2")}},
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-private leaf inside a private group accepted: %v", err)
	}
	// A group the private profile does not write at all.
	if _, err = service.SaveGroup(context.Background(), "routing", GroupRequest{
		RequestID: strings.Repeat("a4", 16),
		Expected:  Expected{Generation: "3", Baseline: strings.Repeat("0", 64)},
		Edits:     []Edit{{Path: "routing.strategy", Action: "set", Value: json.RawMessage(`"fill-first"`)}},
	}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("unwritable private group accepted: %v", err)
	}
	if wire.begins != 0 || wire.puts != 0 || len(wire.patches) != 0 || len(store.record.Config) != 0 {
		t.Fatal("a refused private save reached the Host or the journal")
	}

	// Exactly one set of one shipped leaf is the write.
	operation, err := service.SaveGroup(context.Background(), "retry", GroupRequest{
		RequestID: strings.Repeat("b1", 16),
		Expected:  Expected{Generation: "3", Baseline: baseline(t, service, "retry")},
		Edits:     []Edit{{Path: "routing.retry.request-retry", Action: "set", Value: json.RawMessage("5")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != "persisted" || operation.RequestID != strings.Repeat("b1", 16) ||
		operation.Group != "retry" || len(operation.Edits) != 1 || operation.Edits[0].Value != 5 {
		t.Fatalf("private operation %+v", operation)
	}
	if wire.begins != 1 || wire.puts != 1 {
		t.Fatalf("private save reached the Host %d/%d times", wire.begins, wire.puts)
	}
	if len(wire.patches) != 0 {
		t.Fatal("a private save used the group PATCH path")
	}
	// The evidence lives in the per-field journal, which is what the page reads
	// after a reload.
	journal := store.record.Config["routing.retry.request-retry"]
	if journal == nil || journal.Latest != strings.Repeat("b1", 16) ||
		journal.Operations[journal.Latest].State != "persisted" {
		t.Fatalf("private journal %+v", journal)
	}
}

// TestPrivateProfileReconcilesFromPersistedState proves the unknown original
// survives a page reload: the group view carries the request ID and state, so the
// page can reconcile without any in-memory draft.
func TestPrivateProfileReconcilesFromPersistedState(t *testing.T) {
	service, wire, _, _ := privateFixture(t, false)
	wire.lostPut = true
	request := GroupRequest{
		RequestID: strings.Repeat("c1", 16),
		Expected:  Expected{Generation: "3", Baseline: baseline(t, service, "retry")},
		Edits:     []Edit{{Path: "routing.retry.max-retry-interval", Action: "set", Value: json.RawMessage("60")}},
	}
	if _, err := service.SaveGroup(context.Background(), "retry", request); !errors.Is(err, cpa.ErrApplyUnknown) {
		t.Fatalf("lost Host reply not unknown: %v", err)
	}
	// A fresh read (the reload) still names the original operation.
	view, err := service.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var carried *freshstore.ConfigGroupOperation
	for _, group := range view.Groups {
		if group.ID != "retry" {
			continue
		}
		carried = group.Operation
		if group.Writable || group.Reason != ReasonUnknownOriginal {
			t.Fatalf("group with an unknown original stayed writable: %+v", group)
		}
	}
	if carried == nil || carried.RequestID != request.RequestID || carried.State != "unknown" {
		t.Fatalf("reload did not carry the original operation: %+v", carried)
	}
	// Reconciling from that persisted record resolves it and never resends.
	wire.lostPut = false
	reconciled, err := service.Operation(context.Background(), carried.RequestID, true)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.State != "persisted" || wire.puts != 1 {
		t.Fatalf("reconcile resent the write or lost it: %+v puts=%d", reconciled, wire.puts)
	}
	if view, err = service.Read(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, group := range view.Groups {
		if group.ID == "retry" && (!group.Writable || group.Reason != "") {
			t.Fatalf("group stayed locked after reconcile: %+v", group)
		}
	}
}

// TestPrivateH1ProfileWritesThroughTheHostHandoff proves the H1 profile claims,
// reserves and releases its one step, and that a second write of the same field
// still succeeds: after the release the Host reports the step ID, which the
// Manager must resolve back to its journal request.
func TestPrivateH1ProfileWritesThroughTheHostHandoff(t *testing.T) {
	service, wire, coordinator, store := privateFixture(t, true)
	path := "requests.streaming.keepalive-seconds"
	first := strings.Repeat("d1", 16)
	if _, err := service.SaveGroup(context.Background(), "streaming", GroupRequest{
		RequestID: first,
		Expected:  Expected{Generation: "3", Baseline: baseline(t, service, "streaming")},
		Edits:     []Edit{{Path: path, Action: "set", Value: json.RawMessage("20")}},
	}); err != nil {
		t.Fatalf("first H1 private write failed: %v", err)
	}
	if coordinator.claimed != 1 || coordinator.finished != 1 || wire.puts != 1 {
		t.Fatalf("H1 write reached the Host %d claims, %d finishes, %d puts", coordinator.claimed, coordinator.finished, wire.puts)
	}
	if wire.operation.OperationID != "c:"+first+":0:"+path {
		t.Fatalf("Host did not keep the H1 step: %+v", wire.operation)
	}
	// The field is writable again after its own release.
	view, err := service.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range view.Fields {
		if field.Path == path && !field.Editable {
			t.Fatalf("private field locked after its own H1 write: %+v", field)
		}
	}
	second := strings.Repeat("d2", 16)
	operation, err := service.SaveGroup(context.Background(), "streaming", GroupRequest{
		RequestID: second,
		Expected:  Expected{Generation: "3", Baseline: baseline(t, service, "streaming")},
		Edits:     []Edit{{Path: path, Action: "set", Value: json.RawMessage("45")}},
	})
	if err != nil {
		t.Fatalf("second H1 private write failed: %v", err)
	}
	if operation.State != "persisted" || coordinator.claimed != 2 || wire.puts != 2 {
		t.Fatalf("second H1 write %+v claims=%d puts=%d", operation, coordinator.claimed, wire.puts)
	}
	journal := store.record.Config[path]
	if journal == nil || journal.Latest != second || journal.Operations[second].State != "persisted" {
		t.Fatalf("second H1 write journal %+v", journal)
	}
}

// TestPrivateProfileOutOfRangeValueLandsOnTheField proves the Host's own bounds
// are enforced for the private profile only, and that the refusal names the field
// so the page can show it there instead of as an opaque group failure.
func TestPrivateProfileOutOfRangeValueLandsOnTheField(t *testing.T) {
	service, wire, _, _ := privateFixture(t, false)
	for _, tc := range []struct {
		group string
		edit  Edit
	}{
		{"streaming", Edit{Path: "requests.streaming.keepalive-seconds", Action: "set", Value: json.RawMessage("301")}},
		{"retry", Edit{Path: "routing.retry.request-retry", Action: "set", Value: json.RawMessage("11")}},
		{"streaming", Edit{Path: "requests.streaming.bootstrap-retries", Action: "set", Value: json.RawMessage("-1")}},
	} {
		edit := tc.edit
		_, err := service.SaveGroup(context.Background(), tc.group, GroupRequest{
			RequestID: strings.Repeat("e1", 16),
			Expected:  Expected{Generation: "3", Baseline: baseline(t, service, tc.group)},
			Edits:     []Edit{edit},
		})
		var rejection Rejection
		if !errors.As(err, &rejection) || rejection.Field != edit.Path {
			t.Fatalf("%s=%s refused without naming the field: %v", edit.Path, edit.Value, err)
		}
	}
	// The bounds are the private Host's, and CPA-backed profiles must not copy
	// them: the same value is a shape-valid leaf elsewhere.
	if err := cpa.ValidateConfigLeafValue("requests.streaming.keepalive-seconds", 301); err != nil {
		t.Fatalf("CPA-backed profile inherited the Host bound: %v", err)
	}
	if wire.begins != 0 || wire.puts != 0 {
		t.Fatal("an out-of-range private value reached the Host")
	}
}

// TestPrivateProfileRequiresThePrivateIdentity proves the record must name the
// attested private runtime. A committed record of another profile is never a
// private write path, even when this process was built as the private profile.
func TestPrivateProfileRequiresThePrivateIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity string
		artifact string
	}{
		{"other runtime", "runtime-managed-01", cpa.PrivateKeyArtifact},
		{"other artifact", cpa.PrivateKeyRuntime, "sha256:" + strings.Repeat("b", 64)},
		{"no identity", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, wire, _, _ := privateFixture(t, false)
			service.store = &memoryStore{record: func() freshstore.Record {
				record := managedRecord()
				record.Operation.Strategy = bootstrap.FreshStrategyCheckpoint{
					Strategy: bootstrap.AdoptExisting, Stage: "committed",
					TargetIdentity: tc.identity, TargetArtifactID: tc.artifact, TargetGeneration: 3,
				}
				return record
			}()}
			view, err := service.Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if view.Capability.Writable || view.Capability.Readable || view.Capability.Reason != ReasonNotInstalled {
				t.Fatalf("foreign private identity treated as writable: %+v", view.Capability)
			}
			if _, err = service.SaveGroup(context.Background(), "retry", GroupRequest{
				RequestID: strings.Repeat("f1", 16),
				Expected:  Expected{Generation: "3", Baseline: strings.Repeat("0", 64)},
				Edits:     []Edit{{Path: "routing.retry.request-retry", Action: "set", Value: json.RawMessage("5")}},
			}); !errors.Is(err, cpa.ErrProofUnavailable) {
				t.Fatalf("write on a foreign identity = %v", err)
			}
			if wire.begins != 0 || wire.puts != 0 {
				t.Fatal("a foreign identity reached the Host")
			}
		})
	}
}

// TestPrivateProfileRefusesAnAlreadyTombstonedRequestID proves the per-field
// journal keeps the replay rule: the same request ID with another value is a
// conflict and never a second write.
func TestPrivateProfileRefusesAnAlreadyTombstonedRequestID(t *testing.T) {
	service, wire, _, _ := privateFixture(t, false)
	requestID := strings.Repeat("91", 16)
	request := GroupRequest{
		RequestID: requestID,
		Expected:  Expected{Generation: "3", Baseline: baseline(t, service, "retry")},
		Edits:     []Edit{{Path: "routing.retry.request-retry", Action: "set", Value: json.RawMessage("5")}},
	}
	if _, err := service.SaveGroup(context.Background(), "retry", request); err != nil {
		t.Fatal(err)
	}
	// The original is returned, never re-sent.
	if _, err := service.SaveGroup(context.Background(), "retry", request); err != nil {
		t.Fatalf("identical replay rejected: %v", err)
	}
	if wire.begins != 1 || wire.puts != 1 {
		t.Fatalf("replay wrote again: %d/%d", wire.begins, wire.puts)
	}
	reused := request
	reused.Edits = []Edit{{Path: "routing.retry.request-retry", Action: "set", Value: json.RawMessage("6")}}
	if _, err := service.SaveGroup(context.Background(), "retry", reused); !errors.Is(err, cpa.ErrSourceConflict) {
		t.Fatalf("request ID reused for another value: %v", err)
	}
}
