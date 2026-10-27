package freshconfig

import (
	"context"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

// fakeH1 is the Manager's H1 handoff bookkeeping: one parent, its single step and
// the terminal release. The Runtime keeps the durable parent; the fake wire below
// owns the write.
type fakeH1 struct {
	sourceID   string
	generation string
	sha256     string
	// revision is the recorded source revision the claim and the step follow.
	revision func() uint64
	parent   cpa.H1Parent
	claimed  int
	finished int
}

func (h *fakeH1) Claim(_ context.Context, _ *cpa.Transport, intent cpa.H1Intent) (*cpa.H1Parent, error) {
	h.claimed++
	h.parent = cpa.H1Parent{
		ID: cpa.H1ParentID(intent), Intent: intent, Generation: h.generation, SourceID: h.sourceID,
		ClaimSHA256: h.sha256, ClaimRevision: h.revision() + 1, Phase: "held", Steps: []cpa.H1Step{},
	}
	parent := h.parent
	return &parent, nil
}

func (h *fakeH1) Begin(_ context.Context, _ *cpa.Transport, parent string, n int) (cpa.H1Step, error) {
	step := cpa.H1Step{
		ParentID: parent, SourceID: h.sourceID, Generation: h.generation,
		ID: cpa.H1StepID(h.parent.Intent, n), Scope: cpa.H1IntentStepScope(h.parent.Intent),
		State: "unknown", BeginRevision: h.revision() + 2,
	}
	h.parent.Steps = append(h.parent.Steps, step)
	return step, nil
}

func (h *fakeH1) Commit(_ context.Context, _ *cpa.Transport, _ string, n int) (cpa.H1Step, error) {
	step := h.parent.Steps[n]
	step.State = "persisted"
	// The Runtime returns the step receipt the Manager records as evidence.
	step.Receipt = strings.Repeat("e", 64)
	return step, nil
}

func (h *fakeH1) ParentRecord(_ context.Context, _ *cpa.Transport, _ string) (*cpa.H1Parent, error) {
	parent := h.parent
	return &parent, nil
}

func (h *fakeH1) Finish(_ context.Context, _ *cpa.Transport, _ string, _ string) error {
	h.finished++
	return nil
}

// h1FieldFixture is the private H1 profile: the service claims a Manager-side
// handoff, the wire owns the single step write, and the source reports the step
// that last touched the file after the release.
func h1FieldFixture(t *testing.T, path string, value any) (*Service, *fakeField, *fakeH1, freshstore.Store) {
	t.Helper()
	s, w, st, _ := fieldFixture(t, path, value)
	w.base.H1 = true
	w.base.HandoffValid = true
	w.base.OperationScope = path
	w.base.OperationID = ""
	h1 := &fakeH1{
		sourceID: w.base.SourceID, generation: w.base.Generation, sha256: w.base.SHA256,
		revision: func() uint64 {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.base.Revision
		},
	}
	s.transport = cpa.NewTransport()
	s.h1 = h1
	w.h1 = h1
	return s, w, h1, st
}

// TestH1ConfigFieldSecondWriteSucceeds covers the private H1 profile after its
// first allowlisted write: the Runtime keeps the step ID ("c:<requestId>:0:<path>")
// as its last operation, so the Manager must resolve it back to the journal
// request instead of locking the whole group.
func TestH1ConfigFieldSecondWriteSucceeds(t *testing.T) {
	path := "requests.streaming.keepalive-seconds"
	s, w, h1, st := h1FieldFixture(t, path, 15)
	first := strings.Repeat("a1", 16)
	if _, err := saveField(t, s, path, "20", first); err != nil {
		t.Fatalf("first H1 write failed: %v", err)
	}
	if w.putsH1 != 1 || h1.claimed != 1 || h1.finished != 1 {
		t.Fatalf("first write reached the wire %d times (%d claims, %d finishes)", w.putsH1, h1.claimed, h1.finished)
	}
	// After the release the Runtime keeps the step as its last operation.
	if w.operation.OperationID != "c:"+first+":0:"+path || w.operation.OperationPath != path {
		t.Fatalf("wire source did not keep the H1 step: %+v", w.operation)
	}
	view, err := s.ReadField(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Editable {
		t.Fatalf("the field is locked after its own H1 write: %+v", view)
	}
	second := strings.Repeat("b2", 16)
	operation, err := saveField(t, s, path, "45", second)
	if err != nil {
		t.Fatalf("second H1 write failed: %v", err)
	}
	if operation.State != "persisted" || operation.ParentID != "c:"+second {
		t.Fatalf("second H1 operation %+v", operation)
	}
	if w.putsH1 != 2 || h1.claimed != 2 {
		t.Fatalf("second write did not reach the wire: %d puts, %d claims", w.putsH1, h1.claimed)
	}
	journal := fieldJournalOf(t, st, path)
	if journal == nil || journal.Latest != second || journal.Operations[second].State != "persisted" {
		t.Fatalf("second H1 write journal %+v", journal)
	}
}

// TestH1ConfigFieldWriteAfterKeyStepSucceeds covers the key-scope source: a Key
// add or revoke leaves the Runtime operation with no field, which the old H1 rule
// accepted because the handoff itself was proven.
func TestH1ConfigFieldWriteAfterKeyStepSucceeds(t *testing.T) {
	path := "requests.passthrough-headers"
	s, w, _, _ := h1FieldFixture(t, path, false)
	// The last write was a Key step: its scope names no allowlisted field.
	w.operation = cpa.ConfigFieldSource{OperationID: "c:" + strings.Repeat("1", 32) + ":0:add", OperationScope: "key-add", OperationState: "persisted"}
	view, err := s.ReadField(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Editable {
		t.Fatalf("the field is locked after a Key step: %+v", view)
	}
	if _, err = saveField(t, s, path, "true", strings.Repeat("c3", 16)); err != nil {
		t.Fatalf("write after a Key step failed: %v", err)
	}
	if w.putsH1 != 1 {
		t.Fatalf("write after a Key step reached the wire %d times", w.putsH1)
	}
	// A foreign config step still locks the field: the parent is not in the
	// journal of the field it claims to have written.
	w.operation = cpa.ConfigFieldSource{OperationID: "c:" + strings.Repeat("2", 32) + ":0:" + path, OperationScope: path, OperationPath: path, OperationValue: 30, OperationState: "persisted"}
	view, err = s.ReadField(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if view.Editable {
		t.Fatal("an unknown H1 step left the field writable")
	}
}
