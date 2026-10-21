package cpa

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

// This is an offline transport model, NOT a CPA/Docker proof. Actual Runtime
// state/tree enforcement is independently tested in privateconfig/h1_test.go.
type h1Model struct {
	v     configObservation
	g     configGrant
	drop  string
	calls map[string]int
	db    *sql.DB
}

func (m *h1Model) call(_ context.Context, q configRequest) (configObservation, error) {
	m.calls[q.Action]++
	if q.Action == "observe" {
		return m.clone(), nil
	}
	switch q.Action {
	case "activate":
		m.v.H1 = &H1Record{Version: 2, RunID: m.g.BootstrapRun, Generation: m.v.Generation, InitialConsumed: q.H1.TerminalDigest}
		m.v.Revision++
	case "claim":
		if m.v.H1.Parent != nil || m.g.H1.Action != "claim" || m.g.H1.ParentID != q.H1.ParentID || m.g.H1.Intent != q.H1.Intent || m.g.H1.Revision != m.v.Revision {
			return configObservation{}, ErrSourceConflict
		}
		m.v.Revision++
		m.v.H1.Parent = &H1Parent{ID: q.H1.ParentID, Intent: q.H1.Intent, Generation: m.v.Generation, SourceID: m.v.SourceID, ClaimSHA256: m.v.SHA256, ClaimRevision: m.v.Revision, Phase: "held"}
	case "begin":
		p := m.v.H1.Parent
		m.v.Revision++
		p.Steps = append(p.Steps, H1Step{ParentID: p.ID, SourceID: p.SourceID, Generation: p.Generation, ID: q.OperationID, Scope: q.Scope, State: "unknown", BeforeSHA256: m.v.SHA256, BeginRevision: m.v.Revision, TransitionDigest: hashBytes([]byte(q.OperationID))})
		m.v.State = "unknown"
	case "commit":
		p := m.v.H1.Parent
		n := len(p.Steps) - 1
		s := &p.Steps[n]
		m.v.Revision++
		s.State = "persisted"
		s.AfterSHA256 = hashBytes([]byte(s.ID))
		s.CommitRevision = m.v.Revision
		s.Receipt = h1Digest(*s)
		m.v.SHA256 = s.AfterSHA256
		m.v.State = "persisted"
	case "consume":
		if m.v.H1.Parent.Phase != "terminal-consumed" {
			m.v.H1.Parent.Phase = "terminal-consumed"
			m.v.H1.Parent.TerminalDigest = q.H1.TerminalDigest
			m.v.Revision++
		}
	case "release":
		p := m.v.H1.Parent
		m.v.Revision++
		m.v.H1.Released = H1Released{ParentID: p.ID, Generation: p.Generation, SourceID: p.SourceID, SHA256: m.v.SHA256, Revision: m.v.Revision, TerminalDigest: p.TerminalDigest, Receipts: h1Digest(p.Steps)}
		m.v.H1.Parent = nil
	default:
		return configObservation{}, ErrSourceConflict
	}
	result := m.clone()
	result.Created = q.Action == "claim" || q.Action == "begin"
	if m.drop == q.Action {
		m.drop = ""
		return configObservation{}, ErrApplyUnknown
	}
	return result, nil
}
func (m *h1Model) clone() configObservation {
	b, _ := json.Marshal(m.v)
	var v configObservation
	json.Unmarshal(b, &v)
	return v
}
func h1Fixture(t *testing.T) (*Transport, *H1Coordinator, *h1Model) {
	t.Helper()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "manager.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = sqlite.Migrate(db); e != nil {
		t.Fatal(e)
	}
	m := &h1Model{v: configObservation{SourceID: "h1-source-created-for-test", Generation: "7", Revision: 3, SHA256: strings.Repeat("a", 64), State: "persisted", Installed: true, Operation: configOperation{ID: "s1-install", Scope: "initial-key", State: "persisted", Generation: "7"}}, g: configGrant{SourceID: "h1-source-created-for-test", Generation: "7", BootstrapRun: "h1-fresh-test-run"}, calls: map[string]int{}}
	m.db = db
	h := NewH1Coordinator(db)
	tr := &Transport{h1: h, configurationAuthority: &privateConfigClient{call: m.call, grant: func() (configGrant, error) { return m.g, nil }}}
	if e = h.InitialConsumed(t.Context(), tr, m.g.BootstrapRun, "7", strings.Repeat("1", 32)); e != nil {
		t.Fatal(e)
	}
	m.g.H1 = &H1Grant{RunID: m.g.BootstrapRun, Action: "activate", Revision: 3, SHA256: m.v.SHA256}
	if _, e = h.Ready(t.Context(), tr); e != nil {
		t.Fatal(e)
	}
	return tr, h, m
}
func grantH1(m *h1Model, i H1Intent) {
	m.g.H1 = &H1Grant{RunID: m.g.BootstrapRun, Action: "claim", ParentID: i.parentID(), Intent: i, Revision: m.v.Revision, SHA256: m.v.SHA256}
}
func TestH1ManagerCrossDomainHandoffAndLostReplies(t *testing.T) {
	for _, drop := range []string{"commit", "consume", "release"} {
		t.Run(drop, func(t *testing.T) {
			tr, h, m := h1Fixture(t)
			i := H1Intent{RequestID: strings.Repeat("2", 32), Domain: "config", Mode: "retry", Retry: 0}
			grantH1(m, i)
			p, e := h.Claim(t.Context(), tr, i)
			if e != nil {
				t.Fatal(e)
			}
			step, e := h.Begin(t.Context(), tr, p.ID, 0)
			if e != nil {
				t.Fatal(e)
			}
			if e = h.Reserved(t.Context(), tr, p.ID, step); e != nil {
				t.Fatal(e)
			}
			other := H1Intent{RequestID: strings.Repeat("3", 32), Domain: "key", Mode: "replace", TargetID: strings.Repeat("4", 32), KeeperID: strings.Repeat("5", 32), TargetSHA256: strings.Repeat("6", 64), KeeperSHA256: strings.Repeat("7", 64)}
			if _, e = h.Claim(t.Context(), tr, other); e == nil {
				t.Fatal("other domain entered")
			}
			m.drop = drop
			_, e = h.Commit(t.Context(), tr, p.ID, 0)
			if drop == "commit" && e == nil {
				t.Fatal("lost commit reply accepted")
			}
			if _, e = h.Commit(t.Context(), tr, p.ID, 0); e != nil {
				t.Fatal(e)
			}
			// A durable raw receipt alone never unlocks the source.
			if h.Free(t.Context(), tr) == nil {
				t.Fatal("persisted step released parent")
			}
			terminal := hashBytes([]byte("original durable Config terminal"))
			e = h.Finish(t.Context(), tr, p.ID, terminal)
			if drop != "commit" && e == nil {
				t.Fatal("lost handoff reply accepted")
			}
			if e = h.Finish(t.Context(), tr, p.ID, terminal); e != nil {
				t.Fatal(e)
			}
			if e = h.Free(t.Context(), tr); e != nil {
				t.Fatal(e)
			}
			grantH1(m, other)
			if _, e = h.Claim(t.Context(), tr, other); e != nil {
				t.Fatal("Key cannot accept Config terminal", e)
			}
			if h.Finish(t.Context(), tr, p.ID, terminal) == nil {
				t.Fatal("late Config terminal released Key parent")
			}
			if m.calls["begin"] != 1 || m.calls["commit"] != 1 {
				t.Fatal("replayed step", m.calls)
			}
		})
	}
}
func TestH1ManagerClaimOrphanAndTerminalSQLiteFailure(t *testing.T) {
	for _, phase := range []string{"claim", "terminal"} {
		t.Run(phase, func(t *testing.T) {
			tr, h, m := h1Fixture(t)
			i := H1Intent{RequestID: strings.Repeat("2", 32), Domain: "config", Mode: "retry", Retry: 1}
			grantH1(m, i)
			fail := func() {
				t.Helper()
				if _, e := m.db.Exec(`CREATE TRIGGER fail_handoff BEFORE UPDATE ON settings WHEN NEW.key='fresh_source_handoff_v2' BEGIN SELECT RAISE(FAIL,'injected SQLite failure'); END`); e != nil {
					t.Fatal(e)
				}
			}
			if phase == "claim" {
				fail()
			}
			p, e := h.Claim(t.Context(), tr, i)
			if phase == "claim" {
				if e == nil || m.v.H1.Parent == nil {
					t.Fatal("claim orphan lost", e)
				}
				if h.Free(t.Context(), tr) == nil {
					t.Fatal("orphan bypass")
				}
				if _, e = h.Claim(t.Context(), tr, i); e == nil {
					t.Fatal("orphan reclaimed")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = h.Begin(t.Context(), tr, p.ID, 0); e != nil {
				t.Fatal(e)
			}
			if _, e = h.Commit(t.Context(), tr, p.ID, 0); e != nil {
				t.Fatal(e)
			}
			receipt := m.v.H1.Parent.Steps[0].Receipt
			fail()
			if e = h.Finish(t.Context(), tr, p.ID, hashBytes([]byte("durable original domain terminal"))); e == nil {
				t.Fatal("SQLite failure hidden")
			}
			if m.v.H1.Parent == nil || m.v.H1.Parent.Steps[0].Receipt != receipt || m.calls["consume"] != 0 || m.calls["release"] != 0 {
				t.Fatal("receipt released before handoff checkpoint")
			}
			if h.Free(t.Context(), tr) == nil {
				t.Fatal("terminal checkpoint failure bypass")
			}
		})
	}
}
func TestH1KeySnapshotUsesRawAuthorityAndStrictCommittedProjection(t *testing.T) {
	for _, bad := range []string{"", "wrong-leaf", "missing-leaf", "null-leaf", "extra-root", "provider-drift"} {
		t.Run(bad, func(t *testing.T) {
			tr, h, m := h1Fixture(t)
			_ = h
			value := 0
			m.v.CommittedRetry = &value
			root := strings.Replace(proofConfig, `"api-keys":[]`, `"api-keys":["`+testSecret()+`"]`, 1)
			root = strings.TrimSuffix(root, "}") + `,"routing":{"retry":{"request-retry":0}}}`
			if bad == "wrong-leaf" {
				root = strings.Replace(root, `"request-retry":0`, `"request-retry":1`, 1)
			}
			if bad == "missing-leaf" {
				root = strings.Replace(root, `,"routing":{"retry":{"request-retry":0}}`, "", 1)
			}
			if bad == "null-leaf" {
				root = strings.Replace(root, `"request-retry":0`, `"request-retry":null`, 1)
			}
			if bad == "extra-root" {
				root = strings.TrimSuffix(root, "}") + `,"opaque-unadmitted":false}`
			}
			if bad == "provider-drift" {
				root = strings.TrimSuffix(root, "}") + `,"api-keys":{"openai-compatibility":[]}}`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v8/management/config":
					w.Write([]byte(root))
				case "/v8/management/config/access/api-keys":
					json.NewEncoder(w).Encode([]string{testSecret()})
				default:
					t.Error("GET YAML hash must not be used")
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			tr.client = server.Client()
			tr.endpoint = server.URL
			snap, e := tr.KeySnapshot(t.Context(), testManagement())
			if bad == "" {
				if e != nil || snap.SourceSHA256 != m.v.SHA256 || snap.Revision != m.v.Revision {
					t.Fatal(snap, e)
				}
			} else if e == nil {
				t.Fatal("projection widened seed")
			}
		})
	}
}

func TestH1WrongProfileAndExactRetryDelegation(t *testing.T) {
	tr, h, m := h1Fixture(t)
	tr.h1 = nil
	if _, _, e := tr.configuration(t.Context(), "observe"); e == nil {
		t.Fatal("standalone profile accepted combined authority")
	}
	tr.h1 = h
	i := H1Intent{RequestID: strings.Repeat("2", 32), Domain: "config", Mode: "retry", Retry: 0}
	grantH1(m, i)
	p, e := h.Claim(t.Context(), tr, i)
	if e != nil {
		t.Fatal(e)
	}
	s, e := h.Begin(t.Context(), tr, p.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != retryPath {
			t.Error("unbounded writer", r.Method, r.URL.Path)
		}
		puts++
		w.WriteHeader(200)
	}))
	defer server.Close()
	tr.client = server.Client()
	tr.endpoint = server.URL
	if e = tr.WriteH1Retry(t.Context(), p.ID, s, testManagement(), 1); e == nil || puts != 0 {
		t.Fatal("wrong value reached PUT", e, puts)
	}
	saved := *m.g.H1
	m.g.H1.Intent.RequestID = strings.Repeat("3", 32)
	if e = tr.WriteH1Retry(t.Context(), p.ID, s, testManagement(), 0); e == nil || puts != 0 {
		t.Fatal("different request reached PUT", e, puts)
	}
	*m.g.H1 = saved
	if e = tr.WriteH1Retry(t.Context(), p.ID, s, testManagement(), 0); e != nil || puts != 1 {
		t.Fatal("exact zero delegation denied", e, puts)
	}
	if h.Free(t.Context(), tr) == nil {
		t.Fatal("HTTP success released pending source")
	}
}

func TestH1ManagerB2KeyB2AndRestartBetweenKeySteps(t *testing.T) {
	tr, h, m := h1Fixture(t)
	finish := func(i H1Intent) {
		t.Helper()
		grantH1(m, i)
		p, e := h.Claim(t.Context(), tr, i)
		if e != nil {
			t.Fatal(e)
		}
		for n := range i.scopes() {
			if i.Domain == "key" {
				m.g.H1 = &H1Grant{RunID: m.g.BootstrapRun, Action: "step", ParentID: p.ID, StepID: h1StepID(i, n), Revision: m.v.Revision, SHA256: m.v.SHA256}
			}
			s, e := h.Begin(t.Context(), tr, p.ID, n)
			if e != nil {
				t.Fatal(e)
			}
			if e = h.Reserved(t.Context(), tr, p.ID, s); e != nil {
				t.Fatal(e)
			}
			if _, e = h.Commit(t.Context(), tr, p.ID, n); e != nil {
				t.Fatal(e)
			}
			if i.Domain == "key" && n == 0 {
				h = NewH1Coordinator(m.db)
				tr.UseH1(h)
				if h.Free(t.Context(), tr) == nil {
					t.Fatal("restart released Key parent between steps")
				}
				other := H1Intent{RequestID: strings.Repeat("9", 32), Domain: "config", Mode: "retry", Retry: 1}
				if _, e = h.Claim(t.Context(), tr, other); e == nil {
					t.Fatal("B2 entered between Key steps")
				}
			}
		}
		if e = h.Finish(t.Context(), tr, p.ID, h1Digest(i)); e != nil {
			t.Fatal(e)
		}
	}
	finish(H1Intent{RequestID: strings.Repeat("2", 32), Domain: "config", Mode: "retry", Retry: 1})
	finish(H1Intent{RequestID: strings.Repeat("3", 32), Domain: "key", Mode: "replace", TargetID: strings.Repeat("4", 32), KeeperID: strings.Repeat("5", 32), TargetSHA256: strings.Repeat("6", 64), KeeperSHA256: strings.Repeat("7", 64)})
	finish(H1Intent{RequestID: strings.Repeat("8", 32), Domain: "config", Mode: "retry", Retry: 0})
	if m.calls["begin"] != 4 || m.calls["commit"] != 4 || h.Free(t.Context(), tr) != nil {
		t.Fatal("handoff chain", m.calls)
	}
}
