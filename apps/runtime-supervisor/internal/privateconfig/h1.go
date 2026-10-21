package privateconfig

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
)

// H1 is a bounded private protocol, not a general transaction service. Host
// grants are root-owned files; none of these fields are Public API authority.
type H1Intent struct {
	RequestID    string `json:"requestId"`
	Domain       string `json:"domain"`
	Mode         string `json:"mode"`
	TargetID     string `json:"targetId"`
	KeeperID     string `json:"keeperId"`
	TargetSHA256 string `json:"targetSHA256"`
	KeeperSHA256 string `json:"keeperSHA256"`
	Retry        int    `json:"retry"`
}

func (i H1Intent) parentID() string {
	if i.Domain == "key" {
		return "k:" + i.RequestID
	}
	return "c:" + i.RequestID
}
func (i H1Intent) scopes() []string {
	if i.Domain == "config" {
		return []string{"request-retry"}
	}
	if i.Mode == "replace" {
		return []string{"key-add", "key-revoke"}
	}
	return []string{"key-revoke"}
}
func h1ID(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 16 && hex.EncodeToString(b) == s
}
func (i H1Intent) valid() bool {
	if !h1ID(i.RequestID) {
		return false
	}
	if i.Domain == "config" {
		return i.Mode == "retry" && i.TargetID == "" && i.KeeperID == "" && i.TargetSHA256 == "" && i.KeeperSHA256 == "" && i.Retry >= 0 && i.Retry <= 10
	}
	return i.Domain == "key" && (i.Mode == "replace" || i.Mode == "revoke") && h1ID(i.TargetID) && h1ID(i.KeeperID) && i.TargetID != i.KeeperID && hashValid(i.TargetSHA256) && hashValid(i.KeeperSHA256) && i.TargetSHA256 != i.KeeperSHA256 && i.Retry == 0
}
func h1StepID(i H1Intent, n int) string {
	name := i.scopes()[n]
	if name == "key-add" {
		name = "add"
	}
	if name == "key-revoke" {
		name = "revoke"
	}
	return i.parentID() + ":" + strconv.Itoa(n) + ":" + name
}
func h1Digest(v any) string { b, _ := json.Marshal(v); return digest(b) }

type H1Grant struct {
	RunID    string   `json:"runId"`
	Action   string   `json:"action"`
	ParentID string   `json:"parentId"`
	Intent   H1Intent `json:"intent"`
	StepID   string   `json:"stepId"`
	Revision uint64   `json:"revision"`
	SHA256   string   `json:"sha256"`
}
type H1Request struct {
	ParentID       string   `json:"parentId"`
	Intent         H1Intent `json:"intent"`
	StepID         string   `json:"stepId"`
	Anchor         string   `json:"anchor"`
	TerminalDigest string   `json:"terminalDigest"`
}
type H1Step struct {
	ParentID         string `json:"parentId"`
	SourceID         string `json:"sourceId"`
	Generation       string `json:"generation"`
	ID               string `json:"id"`
	Scope            string `json:"scope"`
	State            string `json:"state"`
	BeforeSHA256     string `json:"beforeSHA256"`
	BeginRevision    uint64 `json:"beginRevision"`
	TransitionDigest string `json:"transitionDigest"`
	AfterSHA256      string `json:"afterSHA256"`
	CommitRevision   uint64 `json:"commitRevision"`
	Receipt          string `json:"receipt"`
}
type H1Parent struct {
	ID             string   `json:"id"`
	Intent         H1Intent `json:"intent"`
	Generation     string   `json:"generation"`
	SourceID       string   `json:"sourceId"`
	ClaimSHA256    string   `json:"claimSHA256"`
	ClaimRevision  uint64   `json:"claimRevision"`
	Phase          string   `json:"phase"`
	TerminalDigest string   `json:"terminalDigest"`
	Steps          []H1Step `json:"steps"`
}
type H1Released struct {
	ParentID       string `json:"parentId"`
	Generation     string `json:"generation"`
	SourceID       string `json:"sourceId"`
	SHA256         string `json:"sha256"`
	Revision       uint64 `json:"revision"`
	TerminalDigest string `json:"terminalDigest"`
	Receipts       string `json:"receipts"`
}
type H1Record struct {
	Version         int        `json:"version"`
	RunID           string     `json:"runId"`
	Generation      string     `json:"generation"`
	InitialConsumed string     `json:"initialConsumed"`
	Parent          *H1Parent  `json:"parent"`
	Released        H1Released `json:"released"`
}

func validH1Record(r *record) bool {
	h := r.H1
	if h == nil || h.Version != 2 || !r.Installed || h.RunID == "" || h.RunID != r.BootstrapRun || h.Generation == "" || !hashValid(h.InitialConsumed) {
		return false
	}
	z := h.Released
	if z != (H1Released{}) && (z.ParentID == "" || z.SourceID != r.SourceID || z.Generation != h.Generation || !hashValid(z.SHA256) || z.Revision < 5 || z.Revision > r.Revision || !hashValid(z.TerminalDigest) || !hashValid(z.Receipts)) {
		return false
	}
	p := h.Parent
	if p == nil {
		return r.Operation.State == "persisted" && r.Operation.Generation == h.Generation && (z == (H1Released{}) || (z.Revision == r.Revision && z.SHA256 == r.LastGoodSHA256))
	}
	if !p.Intent.valid() || p.ID != p.Intent.parentID() || p.SourceID != r.SourceID || p.Generation != h.Generation || !hashValid(p.ClaimSHA256) || p.ClaimRevision == 0 || p.ClaimRevision > r.Revision || (p.Phase != "held" && p.Phase != "terminal-consumed") || len(p.Steps) > len(p.Intent.scopes()) {
		return false
	}
	if z != (H1Released{}) && (p.ClaimRevision <= z.Revision || p.ClaimSHA256 != z.SHA256) {
		return false
	}
	if len(p.Steps) == 0 && r.Operation.State != "persisted" {
		return false
	}
	lastSHA, lastRev := p.ClaimSHA256, p.ClaimRevision
	for n, s := range p.Steps {
		if s.ParentID != p.ID || s.SourceID != p.SourceID || s.Generation != p.Generation || s.ID != h1StepID(p.Intent, n) || s.Scope != p.Intent.scopes()[n] || !hashValid(s.BeforeSHA256) || s.BeginRevision <= p.ClaimRevision || s.BeginRevision > r.Revision || !hashValid(s.TransitionDigest) {
			return false
		}
		if s.BeforeSHA256 != lastSHA || s.BeginRevision <= lastRev {
			return false
		}
		op := Operation{ID: s.ID, Scope: s.Scope, Generation: p.Generation, State: "unknown", Retry: p.Intent.Retry, KeySHA256: p.Intent.TargetSHA256, KeeperSHA256: p.Intent.KeeperSHA256}
		if s.TransitionDigest != h1Digest(op) {
			return false
		}
		if n > 0 && p.Steps[n-1].State != "persisted" {
			return false
		}
		switch s.State {
		case "unknown":
			if n != len(p.Steps)-1 || s.CommitRevision != 0 || s.Receipt != "" || s.AfterSHA256 != "" {
				return false
			}
		case "persisted":
			if !hashValid(s.AfterSHA256) || s.CommitRevision <= s.BeginRevision || s.CommitRevision > r.Revision {
				return false
			}
			copy := s
			copy.Receipt = ""
			if s.Receipt != h1Digest(copy) {
				return false
			}
		case "recovered":
			if n != len(p.Steps)-1 || p.Phase != "held" {
				return false
			}
			if s.Receipt != "" {
				copy := s
				copy.State = "persisted"
				copy.Receipt = ""
				if !hashValid(s.AfterSHA256) || s.CommitRevision <= s.BeginRevision || s.CommitRevision >= r.Revision || s.Receipt != h1Digest(copy) {
					return false
				}
			} else if s.CommitRevision != 0 || s.AfterSHA256 != "" {
				return false
			}
		default:
			return false
		}
		lastRev = s.BeginRevision
		if s.CommitRevision != 0 {
			lastSHA, lastRev = s.AfterSHA256, s.CommitRevision
		}
		if n == len(p.Steps)-1 && (r.Operation.ID != s.ID || r.Operation.Scope != s.Scope || r.Operation.Generation != s.Generation || r.Operation.State != s.State || r.Operation.KeySHA256 != op.KeySHA256 || r.Operation.KeeperSHA256 != op.KeeperSHA256 || r.Operation.Retry != op.Retry) {
			return false
		}
	}
	if r.LastGoodSHA256 != lastSHA || (p.Phase == "held" && p.TerminalDigest != "") {
		return false
	}
	if p.Phase == "terminal-consumed" && (!hashValid(p.TerminalDigest) || !h1Complete(p)) {
		return false
	}
	return true
}
func h1Complete(p *H1Parent) bool {
	if len(p.Steps) != len(p.Intent.scopes()) {
		return false
	}
	for _, s := range p.Steps {
		if s.State != "persisted" {
			return false
		}
	}
	return true
}

func (a *Authority) doH1(q Request, g Grant, r *record, b []byte, running bool) (Result, error) {
	fail := func() (Result, error) { return Result{}, ErrStop }
	sha := digest(b)
	if q.H1 == nil || g.H1 == nil || g.H1.RunID == "" || g.H1.RunID != r.BootstrapRun {
		return fail()
	}
	x, hg := q.H1, g.H1
	if q.Action == "activate" {
		if hg.Action != "activate" {
			return fail()
		}
		if r.Version == 2 {
			if r.H1.InitialConsumed == x.TerminalDigest && r.H1.Generation == g.Generation && sha == r.LastGoodSHA256 {
				return a.result(r, sha, false), nil
			}
			return fail()
		}
		// Bootstrap provenance was written at admit, never retrofitted to an old
		// volume. Exactly one initial-key operation and no intervening B2/Key write.
		if hg.Action != "activate" || r.Version != 1 || r.Revision != 3 || !r.Installed || r.Operation.Scope != "initial-key" || r.Operation.State != "persisted" || r.Operation.Generation != g.Generation || !hashValid(x.TerminalDigest) || q.OperationID != r.Operation.ID || sha != r.LastGoodSHA256 || q.ExpectedSHA256 != sha || hg.SHA256 != sha || q.ExpectedRevision != r.Revision || hg.Revision != r.Revision {
			return fail()
		}
		r.Version = 2
		r.H1 = &H1Record{Version: 2, RunID: hg.RunID, Generation: g.Generation, InitialConsumed: x.TerminalDigest}
		r.Revision++
		if e := a.save(r); e != nil {
			return Result{}, e
		}
		return a.result(r, sha, true), nil
	}
	if r.Version != 2 || r.H1.Generation != g.Generation { // Old generation may only restore while stopped.
		if r.Version == 2 && q.Action == "recover" && !running && hg.Action == "recover" && r.H1.Parent != nil {
			return a.h1Recover(q, g, r, sha)
		}
		return fail()
	}
	h := r.H1
	p := h.Parent
	if q.Action == "claim" {
		if p != nil || h.Released.ParentID == x.ParentID || !running || hg.Action != "claim" || !x.Intent.valid() || x.Intent != hg.Intent || x.ParentID != x.Intent.parentID() || hg.ParentID != x.ParentID || x.Anchor != h1Digest(h.Released) || q.ExpectedRevision != r.Revision || hg.Revision != r.Revision || sha != r.LastGoodSHA256 || q.ExpectedSHA256 != sha || hg.SHA256 != sha {
			return fail()
		}
		r.Revision++
		h.Parent = &H1Parent{ID: x.ParentID, Intent: x.Intent, Generation: g.Generation, SourceID: r.SourceID, ClaimSHA256: sha, ClaimRevision: r.Revision, Phase: "held", Steps: []H1Step{}}
		if e := a.save(r); e != nil {
			return Result{}, e
		}
		return a.result(r, sha, true), nil
	}
	if (q.Action == "release" || q.Action == "consume") && p == nil && h.Released.ParentID == x.ParentID && h.Released.TerminalDigest == x.TerminalDigest && hg.ParentID == x.ParentID && (hg.Action == "reconcile" || hg.Action == "step" || hg.Action == "claim") && sha == h.Released.SHA256 {
		return a.result(r, sha, false), nil
	}
	if p == nil || p.ID != x.ParentID || hg.ParentID != p.ID || (hg.Action != "step" && hg.Action != "reconcile" && hg.Action != "claim" && hg.Action != "recover") {
		return fail()
	}
	if q.Action == "recover" {
		if running {
			return fail()
		}
		return a.h1Recover(q, g, r, sha)
	}
	if q.Action == "begin" {
		n := len(p.Steps)
		if p.Phase != "held" || !running || n >= len(p.Intent.scopes()) || (n > 0 && p.Steps[n-1].State != "persisted") || q.ExpectedRevision != r.Revision || q.ExpectedSHA256 != sha || sha != r.LastGoodSHA256 {
			return fail()
		}
		id := h1StepID(p.Intent, n)
		scope := p.Intent.scopes()[n]
		// Config's exact claim grant also delegates its ONLY scalar step, without
		// adding a Public prepare endpoint. Key steps always need fresh step grants.
		configClaim := p.Intent.Domain == "config" && hg.Action == "claim" && hg.Intent == p.Intent && hg.Revision+1 == p.ClaimRevision && hg.SHA256 == p.ClaimSHA256
		stepGrant := hg.Action == "step" && hg.StepID == id && hg.Revision == r.Revision && hg.SHA256 == sha
		if (!configClaim && !stepGrant) || x.StepID != id || q.OperationID != id || q.Scope != scope {
			return fail()
		}
		op := Operation{ID: id, Scope: scope, Generation: g.Generation, State: "unknown", Retry: p.Intent.Retry, KeySHA256: p.Intent.TargetSHA256, KeeperSHA256: p.Intent.KeeperSHA256}
		r.Revision++
		p.Steps = append(p.Steps, H1Step{ParentID: p.ID, SourceID: p.SourceID, Generation: p.Generation, ID: id, Scope: scope, State: "unknown", BeforeSHA256: sha, BeginRevision: r.Revision, TransitionDigest: h1Digest(op)})
		r.Operation = op
		if e := a.save(r); e != nil {
			return Result{}, e
		}
		return a.result(r, sha, true), nil
	}
	if q.Action == "commit" {
		if len(p.Steps) == 0 || q.OperationID != x.StepID || (!h1TerminalGrant(hg, p) && hg.Action != "recover") {
			return fail()
		}
		for _, prior := range p.Steps {
			if prior.ID == x.StepID && prior.State == "persisted" && r.Operation.State == "persisted" && sha == r.LastGoodSHA256 {
				return a.result(r, sha, false), nil
			}
		}
		if p.Phase != "held" {
			return fail()
		}
		s := &p.Steps[len(p.Steps)-1]
		if s.ID != x.StepID {
			return fail()
		}
		if s.State == "persisted" {
			if sha != s.AfterSHA256 {
				return fail()
			}
			return a.result(r, sha, false), nil
		}
		stopped := !running && hg.Action == "recover" && g.RecoveryOperation == s.ID && g.RecoverySHA256 == sha
		if s.State != "unknown" || (!running && !stopped) || q.ExpectedRevision != r.Revision || q.ExpectedSHA256 != sha || !validChange(r.LastGood, b, r.Operation) {
			return fail()
		}
		if e := a.flushCurrent(g, sha); e != nil {
			return Result{}, e
		}
		r.Revision++
		s.State = "persisted"
		s.AfterSHA256 = sha
		s.CommitRevision = r.Revision
		s.Receipt = h1Digest(*s)
		r.Operation.State = "persisted"
		r.LastGood = b
		r.LastGoodSHA256 = sha
		if s.Scope == "request-retry" {
			n := p.Intent.Retry
			r.CommittedRetry = &n
		}
	} else if q.Action == "consume" {
		if !h1TerminalGrant(hg, p) || !h1Complete(p) || !hashValid(x.TerminalDigest) || sha != r.LastGoodSHA256 {
			return fail()
		}
		if p.Phase == "terminal-consumed" {
			if p.TerminalDigest != x.TerminalDigest {
				return fail()
			}
			return a.result(r, sha, false), nil
		}
		if q.ExpectedRevision != r.Revision || q.ExpectedSHA256 != sha {
			return fail()
		}
		r.Revision++
		p.Phase = "terminal-consumed"
		p.TerminalDigest = x.TerminalDigest
	} else if q.Action == "release" {
		if !h1TerminalGrant(hg, p) || p.Phase != "terminal-consumed" || p.TerminalDigest != x.TerminalDigest || q.ExpectedRevision != r.Revision || q.ExpectedSHA256 != sha || sha != r.LastGoodSHA256 {
			return fail()
		}
		r.Revision++
		h.Released = H1Released{ParentID: p.ID, Generation: p.Generation, SourceID: r.SourceID, SHA256: sha, Revision: r.Revision, TerminalDigest: p.TerminalDigest, Receipts: h1Digest(p.Steps)}
		h.Parent = nil
	} else {
		return fail()
	}
	if e := a.save(r); e != nil {
		return Result{}, e
	}
	return a.result(r, sha, false), nil
}
func h1TerminalGrant(g *H1Grant, p *H1Parent) bool {
	return g.Action == "reconcile" || (p.Intent.Domain == "config" && g.Action == "claim" && g.Intent == p.Intent) || (g.Action == "step" && len(p.Steps) > 0 && g.StepID == p.Steps[len(p.Steps)-1].ID)
}
func (a *Authority) h1Recover(q Request, g Grant, r *record, sha string) (Result, error) {
	p := r.H1.Parent
	if p == nil || p.Phase != "held" || len(p.Steps) == 0 || q.H1.ParentID != p.ID || g.H1.ParentID != p.ID || g.H1.Action != "recover" || q.ExpectedRevision != r.Revision || q.ExpectedSHA256 != sha || g.RecoverySHA256 != sha || g.RecoveryOperation != r.Operation.ID {
		return Result{}, ErrStop
	}
	// Caller must have proved stopped/reaped. The parent and original generation
	// remain occupied even after raw recovery; no Key success or replay follows.
	if e := a.restore(g, r.LastGood, sha); e != nil {
		return Result{}, e
	}
	r.Revision++
	r.Operation.State = "recovered"
	p.Steps[len(p.Steps)-1].State = "recovered"
	if e := a.save(r); e != nil {
		return Result{}, e
	}
	return a.result(r, r.LastGoodSHA256, false), nil
}
