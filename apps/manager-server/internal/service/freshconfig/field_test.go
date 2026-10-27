package freshconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

// fakeField models the Runtime private-configuration journal for one bounded
// field: one reservation slot, one non-replayable PUT and a lost-reply mode.
type fakeField struct {
	mu          sync.Mutex
	base        cpa.ConfigFieldSource
	operation   cpa.ConfigFieldSource
	value       any
	present     bool
	begins      int
	puts        int
	lostPut     bool
	rejectPut   bool
	lostCommit  bool
	failBegin   bool
	applied     bool
	managed     bool
	writes      int
	reads       int
	ignoreWrite bool
	h1          *fakeH1
	putsH1      int
}

func (f *fakeField) source() cpa.ConfigFieldSource {
	out := f.base
	out.OperationID = f.operation.OperationID
	out.OperationScope = f.operation.OperationScope
	out.OperationPath = f.operation.OperationPath
	out.OperationValue = f.operation.OperationValue
	out.OperationState = f.operation.OperationState
	return out
}

func (f *fakeField) ConfigFieldSource(context.Context) (cpa.ConfigFieldSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.source(), nil
}

func (f *fakeField) ReadConfigField(context.Context, string, string) (cpa.ConfigFieldSource, string, any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.present {
		return f.source(), "missing", nil, nil
	}
	return f.source(), "present", f.value, nil
}

func (f *fakeField) BeginConfigField(_ context.Context, _ cpa.ConfigFieldSource, id, path string, value any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.begins++
	if f.failBegin {
		return cpa.ErrProofUnavailable
	}
	if cpa.ValidateConfigFieldValue(path, value) != nil {
		return cpa.ErrProofUnavailable
	}
	f.operation = cpa.ConfigFieldSource{OperationID: id, OperationScope: path, OperationPath: path, OperationValue: value, OperationState: "unknown"}
	f.base.Revision++
	f.base.State = "unknown"
	return nil
}

func (f *fakeField) PutConfigField(_ context.Context, _ cpa.ConfigFieldSource, _ string, _ string, path string, value any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	if f.rejectPut {
		return cpa.ErrApplyUnknown
	}
	f.value, f.present, f.applied = value, true, true
	if f.lostPut {
		return cpa.ErrApplyUnknown
	}
	return nil
}

// WriteH1ConfigField performs the one step write the H1 parent already reserved
// and leaves the step ID as the source's last operation, exactly like the Runtime.
func (f *fakeField) WriteH1ConfigField(_ context.Context, parent string, step cpa.H1Step, _ string, path string, value any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putsH1++
	if f.h1 == nil || parent != f.h1.parent.ID || step.Scope != path || step.ParentID != parent {
		return cpa.ErrProofUnavailable
	}
	if cpa.ValidateConfigFieldValue(path, value) != nil {
		return cpa.ErrProofUnavailable
	}
	f.value, f.present, f.applied = value, true, true
	// The released Runtime keeps the step as its last operation.
	f.operation = cpa.ConfigFieldSource{OperationID: step.ID, OperationScope: path, OperationPath: path, OperationValue: value, OperationState: "persisted"}
	f.base.Revision++
	f.base.State = "persisted"
	return nil
}

func (f *fakeField) HasRuntimeJournal() bool { return !f.managed }

func (f *fakeField) ReadConfigFieldValue(context.Context, string, string) (string, any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if !f.present {
		return "missing", nil, nil
	}
	return "present", f.value, nil
}

func (f *fakeField) WriteConfigFieldValue(_ context.Context, _ string, _ string, value any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if !f.ignoreWrite {
		f.value, f.present, f.applied = value, true, true
	}
	return nil
}

func (f *fakeField) ReconcileConfigField(_ context.Context, source cpa.ConfigFieldSource, requestID, path string, value any) (cpa.ConfigFieldSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.source()
	if out.OperationID != requestID || out.OperationPath != path || !reflect.DeepEqual(out.OperationValue, value) {
		return out, cpa.ErrSourceConflict
	}
	if !f.applied {
		return out, nil
	}
	f.base.State = "persisted"
	f.operation.OperationState = "persisted"
	out = f.source()
	if f.lostCommit {
		return out, cpa.ErrApplyUnknown
	}
	return out, nil
}

func fieldFixture(t *testing.T, path string, value any) (*Service, *fakeField, freshstore.Store, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "manager.sqlite")
	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st := freshstore.New(db)
	id := bootstrap.FreshIdentity{AdmissionID: "admission", InstallationID: "install", Generation: "gen", DatabasePath: dbPath, OperationID: "fresh-operation"}
	admin, err := security.NewAdminCredential("cpamp_FieldPrivateTestAdmin0123456789!", "fresh")
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.Create(t.Context(), id, &admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Update(t.Context(), id, r.Operation.Revision, func(record *freshstore.Record) error {
		record.Operation.Strategy = bootstrap.FreshStrategyCheckpoint{Strategy: bootstrap.NewManaged, Stage: "committed", TargetIdentity: cpa.PrivateKeyRuntime, TargetGeneration: 7, TargetArtifactID: cpa.PrivateKeyArtifact}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w := &fakeField{
		base:    cpa.ConfigFieldSource{SourceID: "isolated-source-1234", Generation: "7", Revision: 2, SHA256: strings.Repeat("a", 64), State: "persisted", Installed: true},
		value:   value,
		present: true,
	}
	w.base.OperationScope = "initial-key"
	s := New(st, func(context.Context, uint64) (string, error) { return "private-test-management", nil })
	s.wire = w
	return s, w, st, db
}

// saveField writes through the same read-then-write path the UI uses: the
// expected source always comes from the current view.
func saveField(t *testing.T, s *Service, path, text string, requestID string) (freshstore.ConfigOperation, error) {
	t.Helper()
	view, err := s.ReadField(t.Context(), path)
	if err != nil {
		return freshstore.ConfigOperation{}, err
	}
	if !view.Editable || view.Expected == nil {
		return freshstore.ConfigOperation{}, cpa.ErrSourceConflict
	}
	q := FieldRequest{RequestID: requestID, Value: []byte(text), Expected: *view.Expected}
	return s.SaveField(t.Context(), path, q)
}

func fieldRequest(t *testing.T, text string) FieldRequest {
	t.Helper()
	return FieldRequest{RequestID: strings.Repeat("d", 32), Value: []byte(text), Expected: Expected{SourceID: "isolated-source-1234", Generation: "7", Revision: 2, SHA256: strings.Repeat("a", 64)}}
}

func TestConfigFieldBoundsTypesAndUnknownPathsRejected(t *testing.T) {
	s, w, _, _ := fieldFixture(t, "requests.streaming.keepalive-seconds", 0)
	for _, tc := range []struct {
		path string
		text string
		ok   bool
	}{
		{"requests.streaming.keepalive-seconds", `0`, true},
		{"requests.streaming.keepalive-seconds", `300`, true},
		{"requests.streaming.keepalive-seconds", `301`, false},
		{"requests.streaming.keepalive-seconds", `-1`, false},
		{"requests.streaming.keepalive-seconds", `1.5`, false},
		{"requests.streaming.keepalive-seconds", `"60"`, false},
		{"requests.streaming.keepalive-seconds", `null`, false},
		{"requests.streaming.keepalive-seconds", `true`, false},
		{"requests.streaming.bootstrap-retries", `0`, true},
		{"requests.streaming.bootstrap-retries", `10`, true},
		{"requests.streaming.bootstrap-retries", `11`, false},
		{"requests.nonstream-keepalive-interval", `300`, true},
		{"requests.nonstream-keepalive-interval", `301`, false},
		{"requests.passthrough-headers", `true`, true},
		{"requests.passthrough-headers", `false`, true},
		{"requests.passthrough-headers", `1`, false},
		{"routing.retry.request-retry", `0`, true},
		{"routing.retry.request-retry", `10`, true},
		{"routing.retry.request-retry", `11`, false},
		{"routing.retry.max-retry-interval", `300`, true},
		{"routing.retry.max-retry-interval", `301`, false},
	} {
		if err := ValidateFieldRequest(tc.path, fieldRequest(t, tc.text)); (err == nil) != tc.ok {
			t.Fatalf("%s=%s accepted=%v want %v", tc.path, tc.text, err == nil, tc.ok)
		}
	}
	for _, path := range []string{"requests.proxy-url", "requests.unknown", "routing.strategy", "requests.streaming.keepalive-seconds.extra", ""} {
		if err := ValidateFieldRequest(path, fieldRequest(t, `1`)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unknown path %q accepted", path)
		}
		if _, err := s.SaveField(t.Context(), path, fieldRequest(t, `1`)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unknown path %q reached the writer", path)
		}
	}
	if w.begins != 0 || w.puts != 0 {
		t.Fatal("invalid input reached the Runtime")
	}
}

func TestConfigFieldJournalIsPerField(t *testing.T) {
	s, _, st, _ := fieldFixture(t, "requests.streaming.keepalive-seconds", 60)
	q := fieldRequest(t, `60`)
	q.RequestID = strings.Repeat("e", 32)
	o, err := s.SaveField(t.Context(), "requests.streaming.keepalive-seconds", q)
	if err != nil || o.State != "persisted" || o.Field != "requests.streaming.keepalive-seconds" {
		t.Fatal(o, err)
	}
	r, err := st.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if r.Config["requests.streaming.keepalive-seconds"] == nil || r.Config["requests.streaming.keepalive-seconds"].Operations[q.RequestID].Value != 60 {
		t.Fatal("journal missing for the saved field")
	}
	for _, path := range []string{"requests.streaming.bootstrap-retries", "requests.passthrough-headers", "routing.retry.request-retry"} {
		if r.Config[path] != nil {
			t.Fatal("save wrote another field journal", path)
		}
	}
	// A new request ID is one more independent write; a replay of the old one is
	// the same tombstone, not a second PUT.
	s, w, _, _ := fieldFixture(t, "requests.streaming.keepalive-seconds", 60)
	replay := fieldRequest(t, `90`)
	replay.RequestID = strings.Repeat("f", 32)
	if _, err = s.SaveField(t.Context(), "requests.streaming.keepalive-seconds", replay); err != nil {
		t.Fatal(err)
	}
	// The exact original request is one tombstone, never a second PUT.
	if _, err = s.SaveField(t.Context(), "requests.streaming.keepalive-seconds", replay); err != nil {
		t.Fatal("idempotent replay rejected", err)
	}
	if w.puts != 1 || w.begins != 1 {
		t.Fatal("replayed mutation", w.puts, w.begins)
	}
	view, err := s.ReadField(t.Context(), "requests.streaming.keepalive-seconds")
	if err != nil {
		t.Fatal(err)
	}
	reused := FieldRequest{RequestID: replay.RequestID, Value: []byte(`91`), Expected: *view.Expected}
	if _, err = s.SaveField(t.Context(), "requests.streaming.keepalive-seconds", reused); !errors.Is(err, cpa.ErrSourceConflict) {
		t.Fatal("request ID reused for a different value", err)
	}
}

func TestConfigFieldUnknownNeverResends(t *testing.T) {
	s, w, st, _ := fieldFixture(t, "requests.passthrough-headers", true)
	q := fieldRequest(t, `true`)
	q.RequestID = strings.Repeat("a1", 16)
	w.lostPut = true
	o, err := s.SaveField(t.Context(), "requests.passthrough-headers", q)
	if !errors.Is(err, cpa.ErrApplyUnknown) || o.State != "unknown" || w.puts != 1 {
		t.Fatal("lost reply did not stop", o, err, w.puts)
	}
	// The same request ID is a tombstone: no second reservation or PUT.
	if _, err = s.SaveField(t.Context(), "requests.passthrough-headers", q); err != nil || w.puts != 1 || w.begins != 1 {
		t.Fatal("original operation replayed", err, w.puts, w.begins)
	}
	other := q
	other.RequestID = strings.Repeat("b1", 16)
	if _, err = s.SaveField(t.Context(), "requests.passthrough-headers", other); !errors.Is(err, cpa.ErrApplyUnknown) || w.puts != 1 {
		t.Fatal("unknown field accepted a new write", err, w.puts)
	}
	// A different field has its own journal and its own reservation slot.
	second, secondWire, _, _ := fieldFixture(t, "requests.streaming.keepalive-seconds", 30)
	free := fieldRequest(t, `30`)
	free.RequestID = strings.Repeat("c1", 16)
	if _, err = second.SaveField(t.Context(), "requests.streaming.keepalive-seconds", free); err != nil || secondWire.puts != 1 {
		t.Fatal("independent field blocked", err, secondWire.puts)
	}
	// The original PUT did land; reconciliation records it without a replay.
	w.lostPut = false
	if o, err = s.Operation(t.Context(), q.RequestID, true); err != nil || o.State != "persisted" || w.puts != 1 {
		t.Fatal("reconcile replayed or lost the original operation", o, err, w.puts)
	}
	r, err := st.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if op := r.Config["requests.passthrough-headers"].Operations[q.RequestID]; op.State != "persisted" {
		t.Fatal("journal did not record recovery", op)
	}
}

func TestConfigFieldLostCommitAndRestartOnlyReconcile(t *testing.T) {
	for _, cut := range []string{"lost-put", "lost-commit", "lost-begin"} {
		t.Run(cut, func(t *testing.T) {
			s, w, st, _ := fieldFixture(t, "routing.retry.max-retry-interval", 120)
			q := fieldRequest(t, `200`)
			q.RequestID = strings.Repeat("ab", 16)
			w.lostPut = cut == "lost-put"
			w.lostCommit = cut == "lost-commit"
			w.failBegin = cut == "lost-begin"
			if _, err := s.SaveField(t.Context(), "routing.retry.max-retry-interval", q); !errors.Is(err, cpa.ErrApplyUnknown) {
				t.Fatal(err)
			}
			// Restart: a fresh consumer over the same SQLite store.
			restarted := New(st, s.target)
			restarted.wire = w
			if _, err := restarted.SaveField(t.Context(), "routing.retry.max-retry-interval", q); err != nil {
				t.Fatal(err)
			}
			next := q
			next.RequestID = strings.Repeat("cd", 16)
			next.Value = []byte(`250`)
			if _, err := restarted.SaveField(t.Context(), "routing.retry.max-retry-interval", next); !errors.Is(err, cpa.ErrApplyUnknown) {
				t.Fatal("unknown did not block the next write", err)
			}
			w.lostCommit = false
			o, err := restarted.Operation(t.Context(), q.RequestID, true)
			if cut == "lost-begin" {
				if err == nil || o.State != "unknown" || w.puts != 0 {
					t.Fatal("pre-PUT uncertainty replayed", o, err, w.puts)
				}
			} else if err != nil || o.State != "persisted" || w.puts != 1 {
				t.Fatal(o, err, w.puts)
			}
			if w.begins != 1 {
				t.Fatal("begin replayed", w.begins)
			}
		})
	}
}

func TestConfigFieldConcurrentConsumersWriteOnce(t *testing.T) {
	s, w, st, _ := fieldFixture(t, "requests.streaming.bootstrap-retries", 2)
	q := fieldRequest(t, `4`)
	q.RequestID = strings.Repeat("ac", 16)
	other := New(st, s.target)
	other.wire = w
	var wg sync.WaitGroup
	for _, consumer := range []*Service{s, other} {
		wg.Add(1)
		go func(consumer *Service) {
			defer wg.Done()
			_, _ = consumer.SaveField(context.Background(), "requests.streaming.bootstrap-retries", q)
		}(consumer)
	}
	wg.Wait()
	if w.begins != 1 || w.puts != 1 {
		t.Fatal("concurrent consumers shared one source", w.begins, w.puts)
	}
}

func TestConfigFieldAdmissionBeforeBegin(t *testing.T) {
	s, w, _, _ := fieldFixture(t, "routing.retry.request-retry", 3)
	q := fieldRequest(t, `0`)
	q.RequestID = strings.Repeat("ad", 16)
	for _, change := range []func(*FieldRequest){
		func(q *FieldRequest) { q.Expected.Revision++ },
		func(q *FieldRequest) { q.Expected.Generation = "8" },
		func(q *FieldRequest) { q.Expected.SourceID = "foreign-source-1234" },
		func(q *FieldRequest) { q.Expected.SHA256 = strings.Repeat("d", 64) },
		func(q *FieldRequest) { q.RequestID = "short" },
	} {
		bad := q
		change(&bad)
		if _, err := s.SaveField(t.Context(), "routing.retry.request-retry", bad); err == nil {
			t.Fatal("invalid admission accepted")
		}
	}
	if w.begins != 0 || w.puts != 0 {
		t.Fatal("invalid input reached begin")
	}
	s.target = func(context.Context, uint64) (string, error) { return "", cpa.ErrProofUnavailable }
	if _, err := s.SaveField(t.Context(), "routing.retry.request-retry", q); err == nil {
		t.Fatal("unproved Host accepted")
	}
	if w.begins != 0 || w.puts != 0 {
		t.Fatal("unproved Host reached begin")
	}
}

func TestConfigFieldCorruptJournalLocksOnlyItsField(t *testing.T) {
	s, _, st, db := fieldFixture(t, "requests.streaming.keepalive-seconds", 60)
	good := fieldRequest(t, `5`)
	good.RequestID = strings.Repeat("d1", 16)
	if _, err := s.SaveField(t.Context(), "requests.streaming.bootstrap-retries", good); err != nil {
		t.Fatal(err)
	}
	r, err := st.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	damaged, err := json.Marshal(map[string]any{"fields": map[string]*freshstore.FieldJournal{
		"requests.streaming.keepalive-seconds": {Latest: good.RequestID, Operations: map[string]freshstore.ConfigOperation{good.RequestID: {
			RequestID: good.RequestID, Field: "requests.streaming.keepalive-seconds", Value: "not-a-number",
			SourceID: good.Expected.SourceID, Generation: good.Expected.Generation, Revision: good.Expected.Revision, SHA256: good.Expected.SHA256, State: "persisted",
		}}},
		"requests.streaming.bootstrap-retries": r.Config["requests.streaming.bootstrap-retries"],
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("update settings set value=? where key='fresh_config_fields_v1'", string(damaged)); err != nil {
		t.Fatal(err)
	}
	if r, err = st.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !r.ConfigInvalid["requests.streaming.keepalive-seconds"] || r.ConfigInvalid["requests.streaming.bootstrap-retries"] {
		t.Fatal("corruption isolation lost", r.ConfigInvalid)
	}
	v, err := s.ReadField(t.Context(), "requests.streaming.keepalive-seconds")
	if err != nil || v.Reason != "configuration_journal_invalid" || v.Editable {
		t.Fatal("corrupt field stayed writable", v, err)
	}
	if v, err = s.ReadField(t.Context(), "requests.streaming.bootstrap-retries"); err != nil || v.Reason == "configuration_journal_invalid" || !v.Editable {
		t.Fatal("healthy field lost write access", v, err)
	}
}

func TestConfigFieldRetryFieldsKeepTheirContract(t *testing.T) {
	s, w, st, _ := fieldFixture(t, "routing.retry.request-retry", 5)
	for path, values := range map[string][]string{
		"routing.retry.request-retry":      {"0", "10"},
		"routing.retry.max-retry-interval": {"0", "300"},
	} {
		for i, text := range values {
			requestID := strings.Repeat(string(rune('a'+i)), 32)
			if _, err := saveField(t, s, path, text, requestID); err != nil {
				t.Fatal(path, text, err)
			}
			if w.operation.OperationPath != path {
				t.Fatal("retry field wrote another path", w.operation.OperationPath)
			}
		}
	}
	r, err := st.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if r.Config["routing.retry.request-retry"] == nil || r.Config["routing.retry.max-retry-interval"] == nil {
		t.Fatal("retry fields not migrated into the table", r.Config)
	}
}
