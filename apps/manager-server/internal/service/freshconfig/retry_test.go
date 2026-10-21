package freshconfig

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

type fakeRetry struct {
	mu                                     sync.Mutex
	source                                 cpa.RetrySource
	begins, puts                           int
	lost, failCommit, failBegin, recovered bool
	value                                  *int
}

type failedTerminalStore struct {
	freshstore.Store
	calls int
}

func (s *failedTerminalStore) Update(ctx context.Context, id bootstrap.FreshIdentity, revision uint64, fn func(*freshstore.Record) error) (freshstore.Record, error) {
	s.calls++
	if s.calls == 2 {
		return freshstore.Record{}, errors.New("synthetic terminal persistence failure")
	}
	return s.Store.Update(ctx, id, revision, fn)
}

func TestRuntimeReceiptSurvivesManagerTerminalPersistenceFailure(t *testing.T) {
	s, w, q := retryFixture(t)
	original := s.store
	s.store = &failedTerminalStore{Store: original}
	o, e := s.Save(t.Context(), q)
	if e == nil || o.State != "unknown" {
		t.Fatal(o, e)
	}
	other := New(original, s.target)
	other.wire = w
	o, e = other.Operation(t.Context(), q.RequestID, true)
	if e != nil || o.State != "persisted" || w.puts != 1 {
		t.Fatal(o, e, w.puts)
	}
}

func TestRetryTombstoneCannotBeDeletedOrRetargeted(t *testing.T) {
	s, _, q := retryFixture(t)
	if _, e := s.Save(t.Context(), q); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*freshstore.Record){func(r *freshstore.Record) { r.Retry = nil }, func(r *freshstore.Record) {
		o := r.Retry.Operations[q.RequestID]
		o.Value = 3
		r.Retry.Operations[q.RequestID] = o
	}, func(r *freshstore.Record) {
		o := r.Retry.Operations[q.RequestID]
		o.State = "unknown"
		r.Retry.Operations[q.RequestID] = o
	}} {
		r, e := s.store.Load(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.store.Update(t.Context(), r.Operation.Identity, r.Operation.Revision, func(r *freshstore.Record) error { change(r); return nil }); !errors.Is(e, freshstore.ErrConflict) {
			t.Fatal("tombstone changed", e)
		}
	}
}

func (w *fakeRetry) RetrySource(context.Context) (cpa.RetrySource, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.source, nil
}
func (w *fakeRetry) ReadRetry(context.Context, string) (cpa.RetrySource, string, *int, error) {
	return w.source, "missing", w.value, nil
}
func (w *fakeRetry) BeginRetry(_ context.Context, src cpa.RetrySource, id string, n int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.begins++
	if w.failBegin {
		return cpa.ErrApplyUnknown
	}
	if w.source != src {
		return cpa.ErrSourceConflict
	}
	w.source.State = "unknown"
	w.source.OperationID = id
	w.source.OperationState = "unknown"
	w.source.OperationScope = "request-retry"
	w.source.OperationRetry = n
	w.source.Revision++
	return nil
}
func (w *fakeRetry) PutRetry(_ context.Context, _ cpa.RetrySource, _ string, _ string, n int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.puts++
	w.value = &n
	if w.lost {
		return cpa.ErrApplyUnknown
	}
	return nil
}
func (w *fakeRetry) ReconcileRetry(_ context.Context, src cpa.RetrySource, id string, n int) (cpa.RetrySource, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failCommit || src.SourceID != w.source.SourceID || src.Generation != w.source.Generation || id != w.source.OperationID || w.value == nil || *w.value != n {
		return w.source, cpa.ErrApplyUnknown
	}
	w.source.State = "persisted"
	w.source.OperationState = "persisted"
	if w.recovered {
		w.source.OperationState = "recovered"
	}
	return w.source, nil
}
func retryFixture(t *testing.T) (*Service, *fakeRetry, Request) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manager.sqlite")
	db, e := sqlite.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	st := freshstore.New(db)
	id := bootstrap.FreshIdentity{AdmissionID: "admission", InstallationID: "install", Generation: "gen", DatabasePath: path, OperationID: "fresh-operation"}
	cred, e := security.NewAdminCredential("cpamp_RetryPrivateTestAdmin0123456789!", "fresh")
	if e != nil {
		t.Fatal(e)
	}
	r, e := st.Create(t.Context(), id, &cred)
	if e != nil {
		t.Fatal(e)
	}
	_, e = st.Update(t.Context(), id, r.Operation.Revision, func(r *freshstore.Record) error {
		r.Operation.Strategy = bootstrap.FreshStrategyCheckpoint{Strategy: bootstrap.NewManaged, Stage: "committed", TargetIdentity: cpa.PrivateKeyRuntime, TargetGeneration: 7, TargetArtifactID: cpa.PrivateKeyArtifact}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	w := &fakeRetry{source: cpa.RetrySource{SourceID: "isolated-source-1234", Generation: "7", Revision: 2, SHA256: strings.Repeat("a", 64), State: "persisted", Installed: true, OperationScope: "initial-key"}}
	s := New(st, func(context.Context, uint64) (string, error) { return "private-test-management", nil })
	s.wire = w
	n := 0
	return s, w, Request{RequestID: strings.Repeat("b", 32), Value: &n, Expected: expected(w.source)}
}
func TestRetryZeroAndDurableIdempotency(t *testing.T) {
	s, w, q := retryFixture(t)
	o, e := s.Save(t.Context(), q)
	if e != nil || o.State != "persisted" {
		t.Fatal(o, e)
	}
	// A separate consumer with the same real SQLite store represents restart.
	other := New(s.store, s.target)
	other.wire = w
	if _, e = other.Save(t.Context(), q); e != nil {
		t.Fatal(e)
	}
	if w.puts != 1 || w.begins != 1 {
		t.Fatal("replayed mutation", w.puts, w.begins)
	}
	n := 1
	q.Value = &n
	if _, e = other.Save(t.Context(), q); !errors.Is(e, cpa.ErrSourceConflict) {
		t.Fatal("requestId reused for different value", e)
	}
	v, e := other.Read(t.Context())
	if e != nil || v.Effective != "unknown" {
		t.Fatal(v, e)
	}
}
func TestLostReplyAndRestartOnlyReconcileOriginal(t *testing.T) {
	for _, cut := range []string{"lost-put", "lost-commit", "lost-begin"} {
		t.Run(cut, func(t *testing.T) {
			s, w, q := retryFixture(t)
			w.lost = cut == "lost-put"
			w.failCommit = cut == "lost-commit"
			w.failBegin = cut == "lost-begin"
			if _, e := s.Save(t.Context(), q); !errors.Is(e, cpa.ErrApplyUnknown) {
				t.Fatal(e)
			}
			other := New(s.store, s.target)
			other.wire = w
			if _, e := other.Save(t.Context(), q); e != nil {
				t.Fatal(e)
			}
			q2 := q
			q2.RequestID = strings.Repeat("c", 32)
			if _, e := other.Save(t.Context(), q2); !errors.Is(e, cpa.ErrApplyUnknown) {
				t.Fatal("unknown did not block next write", e)
			}
			w.failCommit = false
			o, e := other.Operation(t.Context(), q.RequestID, true)
			if cut == "lost-begin" {
				if e == nil || o.State != "unknown" || w.puts != 0 {
					t.Fatal("pre-PUT uncertainty replayed", o, e)
				}
			} else if e != nil || o.State != "persisted" || w.puts != 1 {
				t.Fatal(o, e, w.puts)
			}
			if w.begins != 1 {
				t.Fatal("begin replayed")
			}
		})
	}
}
func TestConcurrentIndependentConsumersOnePUT(t *testing.T) {
	s, w, q := retryFixture(t)
	other := New(s.store, s.target)
	other.wire = w
	var wg sync.WaitGroup
	for _, s := range []*Service{s, other} {
		wg.Add(1)
		go func(s *Service) { defer wg.Done(); s.Save(context.Background(), q) }(s)
	}
	wg.Wait()
	if w.begins != 1 || w.puts != 1 {
		t.Fatal(w.begins, w.puts)
	}
}

func TestDifferentConcurrentRequestsCannotShareSource(t *testing.T) {
	s, w, q := retryFixture(t)
	other := New(s.store, s.target)
	other.wire = w
	q2 := q
	q2.RequestID = strings.Repeat("c", 32)
	var wg sync.WaitGroup
	for i, consumer := range []*Service{s, other} {
		request := q
		if i == 1 {
			request = q2
		}
		wg.Add(1)
		go func() { defer wg.Done(); consumer.Save(context.Background(), request) }()
	}
	wg.Wait()
	if w.puts != 1 || w.begins != 1 {
		t.Fatal(w.puts, w.begins)
	}
}

func TestMissingManagerJournalForRuntimeOperationStops(t *testing.T) {
	s, w, q := retryFixture(t)
	w.source.OperationScope = "request-retry"
	w.source.OperationID = "retry-" + strings.Repeat("d", 32)
	v, e := s.Read(t.Context())
	if e != nil || v.Editable {
		t.Fatal(v, e)
	}
	if _, e = s.Save(t.Context(), q); !errors.Is(e, cpa.ErrSourceConflict) {
		t.Fatal(e)
	}
	if w.begins != 0 || w.puts != 0 {
		t.Fatal("missing journal admitted a new write")
	}
}
func TestRetryAdmissionBeforeBegin(t *testing.T) {
	s, w, q := retryFixture(t)
	for _, n := range []int{-1, 11, 99} {
		q.Value = &n
		if _, e := s.Save(t.Context(), q); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
	q.Value = nil
	if _, e := s.Save(t.Context(), q); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	n := 0
	q.Value = &n
	for _, change := range []func(*Request){func(q *Request) { q.Expected.Revision++ }, func(q *Request) { q.Expected.Generation = "8" }, func(q *Request) { q.Expected.SourceID = "foreign-source-1234" }, func(q *Request) { q.Expected.SHA256 = strings.Repeat("d", 64) }} {
		bad := q
		change(&bad)
		if _, e := s.Save(t.Context(), bad); !errors.Is(e, cpa.ErrSourceConflict) {
			t.Fatal(e)
		}
	}
	s.target = func(context.Context, uint64) (string, error) { return "", cpa.ErrProofUnavailable }
	if _, e := s.Save(t.Context(), q); e == nil {
		t.Fatal("unproved Host accepted")
	}
	if w.begins != 0 || w.puts != 0 {
		t.Fatal("invalid input reached begin")
	}
}
