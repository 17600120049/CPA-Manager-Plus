package cpa

import (
	"context"
	"database/sql"
	"sync"

	repo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
)

// H1Coordinator serializes only Manager handoff bookkeeping. Runtime's durable
// parent is the sole raw-source reservation, including across Manager death.
type H1Coordinator struct {
	mu   sync.Mutex
	repo *repo.Repository
}

func NewH1Coordinator(db *sql.DB) *H1Coordinator { return &H1Coordinator{repo: repo.New(db)} }
func (t *Transport) UseH1(h *H1Coordinator)      { t.h1 = h }

func (h *H1Coordinator) InitialConsumed(ctx context.Context, t *Transport, run, gen, keyID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, v, e := t.configuration(ctx, "observe")
	if e != nil {
		return e
	}
	if v.H1 != nil {
		return nil
	}
	g, e := t.configurationAuthority.grant()
	if e != nil || g.BootstrapRun != run || v.Generation != gen || v.State != "persisted" || !v.Installed || v.Operation.Scope != "initial-key" || v.Operation.State != "persisted" || v.Revision != 3 {
		return ErrSourceConflict
	}
	if e = h.repo.H1Idle(ctx); e != nil {
		return e
	}
	initial := h1Digest(struct{ Run, Generation, Key, Source, Raw, Operation string }{run, gen, keyID, v.SourceID, v.SHA256, v.Operation.ID})
	old, e := h.repo.Handoff(ctx)
	if e != nil {
		return e
	}
	if old != nil {
		if old.InitialDigest == initial && old.SourceID == v.SourceID && old.Generation == gen && old.RunID == run {
			return nil
		}
		return ErrSourceConflict
	}
	return h.repo.SaveHandoff(ctx, nil, repo.Handoff{Version: 2, RunID: run, SourceID: v.SourceID, Generation: gen, InitialDigest: initial, Anchor: h1Digest(H1Released{}), Phase: "free"})
}

// Ready may perform the single Host-authorized bootstrap handoff. Public
// input cannot mint that grant or consume an unknown S1 install.
func (h *H1Coordinator) Ready(ctx context.Context, t *Transport) (configObservation, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ready(ctx, t)
}
func (h *H1Coordinator) ready(ctx context.Context, t *Transport) (configObservation, error) {
	q, v, e := t.configuration(ctx, "observe")
	if e != nil {
		return v, e
	}
	cp, e := h.repo.Handoff(ctx)
	if e != nil || cp == nil {
		return v, ErrSourceConflict
	}
	if v.SourceID != cp.SourceID || v.Generation != cp.Generation {
		return v, ErrSourceConflict
	}
	if v.H1 == nil {
		if e = h.repo.H1Idle(ctx); e != nil {
			return v, e
		}
		q.Action = "activate"
		q.OperationID = v.Operation.ID
		q.ExpectedSHA256 = v.SHA256
		q.ExpectedRevision = v.Revision
		q.H1 = &H1Request{TerminalDigest: cp.InitialDigest}
		v, e = t.configurationAuthority.call(ctx, q)
		if e != nil {
			return v, e
		}
	}
	if v.H1 == nil || v.H1.Version != 2 || v.H1.RunID != cp.RunID || v.H1.Generation != cp.Generation || v.H1.InitialConsumed != cp.InitialDigest {
		return v, ErrSourceConflict
	}
	return v, nil
}
func (h *H1Coordinator) Free(ctx context.Context, t *Transport) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, bootstrap, err := t.configuration(ctx, "observe")
	if err != nil {
		return err
	}
	if bootstrap.H1 == nil && bootstrap.State == "persisted" && bootstrap.Operation.Scope == "initial-key" {
		g, e := t.configurationAuthority.grant()
		if e == nil && g.H1 == nil && g.BootstrapRun != "" {
			return h.repo.H1Idle(ctx)
		}
	}
	v, e := h.ready(ctx, t)
	if e != nil {
		return e
	}
	cp, e := h.repo.Handoff(ctx)
	if e != nil || cp == nil || cp.Phase != "free" || v.H1.Parent != nil || cp.Anchor != h1Digest(v.H1.Released) || v.State != "persisted" {
		return ErrApplyUnknown
	}
	return h.repo.H1Idle(ctx)
}
func (h *H1Coordinator) Claim(ctx context.Context, t *Transport, i H1Intent) (*H1Parent, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, e := h.ready(ctx, t)
	if e != nil {
		return nil, e
	}
	cp, e := h.repo.Handoff(ctx)
	if e != nil || cp == nil || cp.Phase != "free" || v.H1.Parent != nil || cp.Anchor != h1Digest(v.H1.Released) || v.State != "persisted" || !i.valid() {
		return nil, ErrSourceConflict
	}
	if e = h.repo.H1Idle(ctx); e != nil {
		return nil, e
	}
	q := configRequest{Action: "claim", SourceID: v.SourceID, Generation: v.Generation, ExpectedRevision: v.Revision, ExpectedSHA256: v.SHA256, H1: &H1Request{ParentID: i.parentID(), Intent: i, Anchor: cp.Anchor}}
	got, e := t.configurationAuthority.call(ctx, q)
	// Intent equality is digest equality: a scalar Value crosses the Runtime JSON
	// boundary and must not be compared as a Go dynamic type.
	if e != nil || !got.Created || got.H1 == nil || got.H1.Parent == nil || got.H1.Parent.ID != i.parentID() || h1Digest(got.H1.Parent.Intent) != h1Digest(i) {
		return nil, ErrApplyUnknown
	}
	next := *cp
	next.ParentID = i.parentID()
	next.IntentDigest = h1Digest(i)
	next.Phase = "held"
	next.TerminalDigest = ""
	next.Receipts = ""
	if e = h.repo.SaveHandoff(ctx, cp, next); e != nil {
		return nil, e
	}
	return got.H1.Parent, nil
}
func (h *H1Coordinator) Parent(ctx context.Context, t *Transport, id string) (configObservation, error) {
	v, e := h.Ready(ctx, t)
	if e != nil {
		return v, e
	}
	cp, e := h.repo.Handoff(ctx)
	if e != nil || cp == nil || cp.ParentID != id || cp.Phase == "free" || v.H1.Parent == nil || v.H1.Parent.ID != id || cp.IntentDigest != h1Digest(v.H1.Parent.Intent) {
		return v, ErrApplyUnknown
	}
	return v, nil
}

// ParentRecord returns the held H1 parent the handoff currently owns. It is the
// exported shape a field service verifies its own journal against.
func (h *H1Coordinator) ParentRecord(ctx context.Context, t *Transport, id string) (*H1Parent, error) {
	v, e := h.Parent(ctx, t, id)
	if e != nil {
		return nil, e
	}
	return v.H1.Parent, nil
}
func (h *H1Coordinator) Begin(ctx context.Context, t *Transport, parent string, n int) (H1Step, error) {
	v, e := h.Parent(ctx, t, parent)
	if e != nil {
		return H1Step{}, e
	}
	p := v.H1.Parent
	if n < 0 || n >= len(p.Intent.scopes()) || len(p.Steps) != n {
		return H1Step{}, ErrApplyUnknown
	}
	id := h1StepID(p.Intent, n)
	got, e := t.configurationAuthority.call(ctx, configRequest{Action: "begin", SourceID: v.SourceID, Generation: v.Generation, ExpectedRevision: v.Revision, ExpectedSHA256: v.SHA256, OperationID: id, Scope: p.Intent.scopes()[n], H1: &H1Request{ParentID: parent, StepID: id}})
	if e != nil || !got.Created || got.H1 == nil || got.H1.Parent == nil || len(got.H1.Parent.Steps) != n+1 {
		return H1Step{}, ErrApplyUnknown
	}
	s := got.H1.Parent.Steps[n]
	if s.ID != id || s.ParentID != parent || s.SourceID != v.SourceID || s.Generation != v.Generation || s.State != "unknown" {
		return s, ErrApplyUnknown
	}
	return s, nil
}
func (h *H1Coordinator) Reserved(ctx context.Context, t *Transport, parent string, step H1Step) error {
	v, e := h.Parent(ctx, t, parent)
	if e != nil {
		return e
	}
	p := v.H1.Parent
	if len(p.Steps) == 0 || p.Steps[len(p.Steps)-1] != step || step.State != "unknown" || v.Revision != step.BeginRevision || v.SHA256 != step.BeforeSHA256 {
		return ErrApplyUnknown
	}
	// Re-read Host authority immediately before PUT; expiration is not cancellation
	// of an already issued request. Runtime retains the reservation in either case.
	g, e := t.configurationAuthority.grant()
	if e != nil || g.H1 == nil || g.H1.ParentID != parent || (!(g.H1.Action == "step" && g.H1.StepID == step.ID && g.H1.Revision+1 == step.BeginRevision && g.H1.SHA256 == step.BeforeSHA256) && !(p.Intent.Domain == "config" && g.H1.Action == "claim" && h1Digest(g.H1.Intent) == h1Digest(p.Intent) && g.H1.Revision+1 == p.ClaimRevision && g.H1.SHA256 == p.ClaimSHA256)) {
		return ErrApplyUnknown
	}
	return nil
}

// Commit is reachable only after the original domain journal durably records
// HTTP200. This method never performs a CPA PUT, including on a lost reply.
func (h *H1Coordinator) Commit(ctx context.Context, t *Transport, parent string, n int) (H1Step, error) {
	v, e := h.Parent(ctx, t, parent)
	if e != nil {
		return H1Step{}, e
	}
	p := v.H1.Parent
	if n < 0 || n >= len(p.Steps) {
		return H1Step{}, ErrApplyUnknown
	}
	s := p.Steps[n]
	if s.State == "persisted" {
		if v.State != "persisted" {
			return s, ErrApplyUnknown
		}
		return s, nil
	}
	if s.State != "unknown" || n != len(p.Steps)-1 {
		return s, ErrApplyUnknown
	}
	got, e := t.configurationAuthority.call(ctx, configRequest{Action: "commit", SourceID: v.SourceID, Generation: v.Generation, ExpectedRevision: v.Revision, ExpectedSHA256: v.SHA256, OperationID: s.ID, H1: &H1Request{ParentID: parent, StepID: s.ID}})
	if e != nil || got.H1 == nil || got.H1.Parent == nil || got.H1.Parent.Steps[n].State != "persisted" {
		return s, ErrApplyUnknown
	}
	return got.H1.Parent.Steps[n], nil
}

// Finish is called AFTER the immutable domain terminal is durable. A crash
// leaves either its original receipts or LastReleased; never replayable PUT.
func (h *H1Coordinator) Finish(ctx context.Context, t *Transport, parent, terminal string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, e := h.ready(ctx, t)
	if e != nil {
		return e
	}
	cp, e := h.repo.Handoff(ctx)
	if e != nil || cp == nil {
		return ErrApplyUnknown
	}
	if v.H1.Parent == nil {
		z := v.H1.Released
		if cp.ParentID != parent && cp.Phase == "free" && cp.Anchor == h1Digest(z) && v.State == "persisted" {
			return nil
		}
		if z.ParentID != parent || z.TerminalDigest != terminal || z.SourceID != cp.SourceID || z.Generation != cp.Generation || z.SHA256 != v.SHA256 || cp.ParentID != parent || cp.TerminalDigest != terminal {
			return ErrApplyUnknown
		}
		next := *cp
		next.Phase = "free"
		next.Anchor = h1Digest(z)
		return h.repo.SaveHandoff(ctx, cp, next)
	}
	p := v.H1.Parent
	if p.ID != parent || p.SourceID != cp.SourceID || p.Generation != cp.Generation || cp.ParentID != parent || cp.IntentDigest != h1Digest(p.Intent) || (cp.TerminalDigest != "" && cp.TerminalDigest != terminal) || len(p.Steps) != len(p.Intent.scopes()) || (cp.Phase == "terminal" && cp.Receipts != h1Digest(p.Steps)) {
		return ErrApplyUnknown
	}
	for _, s := range p.Steps {
		if s.State != "persisted" {
			return ErrApplyUnknown
		}
	}
	next := *cp
	next.Phase = "terminal"
	next.TerminalDigest = terminal
	next.Receipts = h1Digest(p.Steps)
	if next != *cp {
		if e = h.repo.SaveHandoff(ctx, cp, next); e != nil {
			return e
		}
		// Keep the durable CAS before-image separate from the later free value.
		// Aliasing next here would change BOTH sides before SaveHandoff.
		durable := next
		cp = &durable
	}
	for _, action := range []string{"consume", "release"} {
		got, err := t.configurationAuthority.call(ctx, configRequest{Action: action, SourceID: v.SourceID, Generation: v.Generation, ExpectedRevision: v.Revision, ExpectedSHA256: v.SHA256, H1: &H1Request{ParentID: parent, TerminalDigest: terminal}})
		if err != nil {
			return ErrApplyUnknown
		}
		v = got
	}
	if v.H1 == nil || v.H1.Parent != nil || v.H1.Released.ParentID != parent || v.H1.Released.TerminalDigest != terminal {
		return ErrApplyUnknown
	}
	next = *cp
	next.Phase = "free"
	next.Anchor = h1Digest(v.H1.Released)
	return h.repo.SaveHandoff(ctx, cp, next)
}
func H1Terminal(v any) string           { return h1Digest(v) }
func H1SecretHash(v string) string      { return hashBytes([]byte(v)) }
func H1ParentID(i H1Intent) string      { return i.parentID() }
func H1StepID(i H1Intent, n int) string { return h1StepID(i, n) }
