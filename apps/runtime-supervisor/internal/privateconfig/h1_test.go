package privateconfig

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Independent Go modules intentionally carry local wire types. Verify field
// types/order/JSON tags too: receipt digests must match, not merely JSON decode.
func TestH1ManagerRuntimeWireCompatibility(t *testing.T) {
	read := func(path string) map[string]string {
		t.Helper()
		fset := token.NewFileSet()
		f, e := parser.ParseFile(fset, path, nil, 0)
		if e != nil {
			t.Fatal(e)
		}
		out := map[string]string{}
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.TYPE {
				continue
			}
			for _, s := range g.Specs {
				v := s.(*ast.TypeSpec)
				if strings.HasPrefix(v.Name.Name, "H1") {
					var b bytes.Buffer
					if e = format.Node(&b, fset, v); e != nil {
						t.Fatal(e)
					}
					out[v.Name.Name] = b.String()
				}
			}
		}
		return out
	}
	local := read("h1.go")
	remote := read("../../../manager-server/internal/service/cpa/h1_types.go")
	if len(local) != len(remote) || len(local) != 7 {
		t.Fatal("wire type inventory drift")
	}
	for name, want := range local {
		if remote[name] != want {
			t.Fatal("wire/receipt digest schema drift", name)
		}
	}
}

const oldH1Key = "cpamp_sk_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const newH1Key = "cpamp_sk_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func h1Fresh(t *testing.T) *fixture {
	f := freshBootstrap(t, "h1-disposable-test-run")
	q := f.q("begin")
	v := f.observe()
	q.ExpectedRevision = v.Revision
	q.ExpectedSHA256 = v.SHA256
	q.OperationID = "s1-install"
	q.Scope = "initial-key"
	q.KeySHA256 = digest([]byte(oldH1Key))
	v = f.do(q, true)
	q.ExpectedRevision = v.Revision
	f.write([]byte(strings.Replace(string(f.seed), "api-keys: []", "api-keys: ['"+oldH1Key+"']", 1)))
	f.commit(q)
	v = f.observe()
	f.g.H1 = &H1Grant{RunID: f.g.BootstrapRun, Action: "activate", Revision: v.Revision, SHA256: v.SHA256}
	f.grant()
	q = f.q("activate")
	q.OperationID = v.Operation.ID
	q.ExpectedRevision = v.Revision
	q.ExpectedSHA256 = v.SHA256
	q.H1 = &H1Request{TerminalDigest: digest([]byte("durable Manager S1 terminal"))}
	f.do(q, true)
	return f
}
func (f *fixture) h1Claim(i H1Intent) Result {
	f.t.Helper()
	v := f.observe()
	f.g.H1 = &H1Grant{RunID: f.g.BootstrapRun, Action: "claim", ParentID: i.parentID(), Intent: i, Revision: v.Revision, SHA256: v.SHA256}
	f.grant()
	q := f.q("claim")
	q.ExpectedRevision = v.Revision
	q.ExpectedSHA256 = v.SHA256
	q.H1 = &H1Request{ParentID: i.parentID(), Intent: i, Anchor: h1Digest(v.H1.Released)}
	return f.do(q, true)
}
func (f *fixture) h1Begin(n int) Request {
	f.t.Helper()
	v := f.observe()
	p := v.H1.Parent
	id := h1StepID(p.Intent, n)
	if p.Intent.Domain == "key" {
		f.g.H1 = &H1Grant{RunID: f.g.BootstrapRun, Action: "step", ParentID: p.ID, StepID: id, Revision: v.Revision, SHA256: v.SHA256}
		f.grant()
	}
	q := f.q("begin")
	q.ExpectedRevision = v.Revision
	q.ExpectedSHA256 = v.SHA256
	q.OperationID = id
	q.Scope = p.Intent.scopes()[n]
	q.H1 = &H1Request{ParentID: p.ID, StepID: id}
	v = f.do(q, true)
	q.ExpectedRevision = v.Revision
	return q
}
func (f *fixture) h1Finish() Request {
	f.t.Helper()
	v := f.observe()
	p := v.H1.Parent
	q := f.q("consume")
	q.ExpectedRevision = v.Revision
	q.ExpectedSHA256 = v.SHA256
	q.H1 = &H1Request{ParentID: p.ID, TerminalDigest: digest([]byte("durable domain terminal"))}
	v = f.do(q, true)
	q.Action = "release"
	q.ExpectedRevision = v.Revision
	f.do(q, true)
	return q
}
func keyIntent(mode string) H1Intent {
	return H1Intent{RequestID: strings.Repeat("1", 32), Domain: "key", Mode: mode, TargetID: strings.Repeat("2", 32), KeeperID: strings.Repeat("3", 32), TargetSHA256: digest([]byte(oldH1Key)), KeeperSHA256: digest([]byte(newH1Key))}
}
func retryIntent(id string, n int) H1Intent {
	return H1Intent{RequestID: strings.Repeat(id, 32), Domain: "config", Mode: "retry", Retry: n}
}
func TestH1ReplacementHoldsReceiptsThroughTerminalRelease(t *testing.T) {
	f := h1Fresh(t)
	f.h1Claim(keyIntent("replace"))
	if f.a.CheckStartup() == nil {
		t.Fatal("active parent allowed spawn")
	}
	q := f.h1Begin(0)
	b, _ := os.ReadFile(f.a.config)
	f.write([]byte(strings.Replace(string(b), oldH1Key+"']", oldH1Key+"', '"+newH1Key+"']", 1)))
	f.commit(q)
	v := f.observe()
	if len(v.H1.Parent.Steps) != 1 || v.H1.Parent.Steps[0].Receipt == "" {
		t.Fatal("missing add receipt")
	}
	other := f.q("claim")
	other.H1 = &H1Request{ParentID: "c:" + strings.Repeat("9", 32), Intent: retryIntent("9", 0)}
	if _, e := f.a.Do(other, true); e == nil {
		t.Fatal("B2 entered between add and revoke")
	}
	rebuilt := New(f.a.root, f.a.config, f.a.grant, f.a.namespace, 7, uint32(os.Geteuid()))
	f.a = rebuilt
	if f.a.CheckStartup() == nil {
		t.Fatal("restart lost parent")
	}
	q = f.h1Begin(1)
	b, _ = os.ReadFile(f.a.config)
	f.write([]byte(strings.Replace(string(b), "'"+oldH1Key+"', ", "", 1)))
	f.commit(q)
	v = f.observe()
	if len(v.H1.Parent.Steps) != 2 {
		t.Fatal("lost earlier receipt")
	}
	release := f.h1Finish()
	v = f.observe()
	if v.H1.Parent != nil || v.H1.Released.Receipts == "" {
		t.Fatal("bad release")
	}
	// Same release is harmless; a late release cannot unlock a subsequent parent.
	f.do(release, true)
	f.g.H1.Action = "reconcile"
	f.grant()
	f.do(release, true)
	f.h1Claim(retryIntent("4", 0))
	if _, e := f.a.Do(release, true); e == nil {
		t.Fatal("late release cleared new owner")
	}
}
func TestH1ConcurrentClaimAndConfigSameValue(t *testing.T) {
	f := h1Fresh(t)
	v := f.observe()
	i := retryIntent("4", 0)
	f.g.H1 = &H1Grant{RunID: f.g.BootstrapRun, Action: "claim", ParentID: i.parentID(), Intent: i, Revision: v.Revision, SHA256: v.SHA256}
	f.grant()
	q := f.q("claim")
	q.ExpectedRevision = v.Revision
	q.ExpectedSHA256 = v.SHA256
	q.H1 = &H1Request{ParentID: i.parentID(), Intent: i, Anchor: h1Digest(v.H1.Released)}
	var count atomic.Int32
	var wg sync.WaitGroup
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := f.a.Do(q, true); e == nil {
				count.Add(1)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatal(count.Load())
	}
	begin := f.h1Begin(0)
	f.write(f.changed("routing", "retry", "request-retry"))
	f.commit(begin)
	f.h1Finish()
	f.h1Claim(retryIntent("5", 0))
	begin = f.h1Begin(0)
	f.commit(begin)
	f.h1Finish()
	if f.observe().H1.Released.ParentID != "c:"+strings.Repeat("5", 32) {
		t.Fatal("same-value write missing receipt")
	}
}
func TestH1TakeoverRejectsLegacyUnknownAndWrongGeneration(t *testing.T) {
	f := fresh(t)
	f.install()
	v := f.observe()
	f.g.H1 = &H1Grant{RunID: "h1-forged-run", Action: "activate", Revision: v.Revision, SHA256: v.SHA256}
	f.grant()
	q := f.q("activate")
	q.ExpectedRevision = v.Revision
	q.ExpectedSHA256 = v.SHA256
	q.OperationID = v.Operation.ID
	q.H1 = &H1Request{TerminalDigest: digest([]byte("terminal"))}
	if _, e := f.a.Do(q, true); e == nil {
		t.Fatal("retrofitted old source")
	}
	f = h1Fresh(t)
	f.h1Claim(keyIntent("replace"))
	f.h1Begin(0)
	f.g.Generation = "8"
	f.grant()
	f.a = New(f.a.root, f.a.config, f.a.grant, f.a.namespace, 8, uint32(os.Geteuid()))
	if f.a.CheckStartup() == nil {
		t.Fatal("new generation spawned with parent")
	}
	q = f.q("claim")
	q.H1 = &H1Request{ParentID: "new"}
	if _, e := f.a.Do(q, true); e == nil {
		t.Fatal("new generation took over")
	}
}
func TestH1ExpiryAndStoppedRecoveryRetainParent(t *testing.T) {
	f := h1Fresh(t)
	f.h1Claim(retryIntent("4", 0))
	q := f.h1Begin(0)
	f.write([]byte("config-version:"))
	v := f.observe()
	f.g.ExpiresAtMS = time.Now().Add(-time.Millisecond).UnixMilli()
	f.grant()
	q.Action = "commit"
	q.ExpectedSHA256 = v.SHA256
	if _, e := f.a.Do(q, true); e == nil {
		t.Fatal("expired grant admitted action")
	}
	f.g.ExpiresAtMS = time.Now().Add(time.Minute).UnixMilli()
	f.g.H1.Action = "recover"
	f.g.RecoveryOperation = q.OperationID
	f.g.RecoverySHA256 = v.SHA256
	f.grant()
	q.Action = "recover"
	if _, e := f.a.Do(q, true); e == nil {
		t.Fatal("live raw recovery")
	}
	f.do(q, false)
	v = f.observe()
	if v.H1.Parent == nil || v.H1.Parent.Steps[0].State != "recovered" || f.a.CheckStartup() == nil {
		t.Fatal("recovery released parent")
	}
	raw, _ := os.ReadFile(f.a.root + "/record.json")
	var legacy struct {
		Version        int       `json:"version"`
		SourceID       string    `json:"sourceId"`
		SeedSHA256     string    `json:"seedSHA256"`
		Device         uint64    `json:"device"`
		Inode          uint64    `json:"inode"`
		Revision       uint64    `json:"revision"`
		Installed      bool      `json:"installed"`
		CommittedRetry *int      `json:"committedRetry"`
		LastGood       []byte    `json:"lastGood"`
		LastGoodSHA256 string    `json:"lastGoodSHA256"`
		Operation      Operation `json:"operation"`
	}
	if strict(raw, &legacy) == nil {
		t.Fatal("old reader accepted v2")
	}
	var decoded record
	if json.Unmarshal(raw, &decoded) != nil || !validH1Record(&decoded) {
		t.Fatal("invalid recovery record")
	}
}

func TestH1ExactKeyTreeChangesAndOrder(t *testing.T) {
	keyTree := func(keys string) []byte {
		return []byte("config-version: 8\naccess:\n  api-keys: [" + keys + "]\nmanagement:\n  secret-key: hash-secret\napi-keys:\n  openai-compatibility:\n    - name: provider\n      keys: [{api-key: provider-secret}]\nopaque: {plugin: [a, null, 0]}\n")
	}
	quotedOld, quotedNew := "'"+oldH1Key+"'", "'"+newH1Key+"'"
	add := Operation{Scope: "key-add", KeySHA256: digest([]byte(oldH1Key)), KeeperSHA256: digest([]byte(newH1Key))}
	if !validChange(keyTree(quotedOld), keyTree(quotedOld+","+quotedNew), add) {
		t.Fatal("exact append denied")
	}
	if validChange(keyTree(quotedOld), keyTree(quotedNew+","+quotedOld), add) {
		t.Fatal("add reordered target")
	}
	revoke := add
	revoke.Scope = "key-revoke"
	for _, order := range []string{quotedOld + "," + quotedNew, quotedNew + "," + quotedOld} {
		before, after := keyTree(order), keyTree(quotedNew)
		if !validChange(before, after, revoke) {
			t.Fatal("revoke order", order)
		}
		for _, part := range []string{"provider-secret", "hash-secret", "plugin: [a, null, 0]"} {
			if validChange(before, []byte(strings.Replace(string(after), part, part+"-changed", 1)), revoke) {
				t.Fatal("non-key mutation accepted", part)
			}
		}
		for _, bad := range []string{"", quotedOld, quotedNew + "," + quotedNew} {
			if validChange(before, keyTree(bad), revoke) {
				t.Fatal("wrong/last key removed", bad)
			}
		}
	}
	if validChange(keyTree(quotedOld), keyTree(""), revoke) {
		t.Fatal("last key removed")
	}
	if validChange(keyTree(quotedOld), keyTree(quotedOld+","+quotedNew), Operation{Scope: "initial-key", KeySHA256: add.KeeperSHA256}) {
		t.Fatal("initial grant reused")
	}
}

func TestH1GrantIdentityAndReceiptCorruption(t *testing.T) {
	for _, field := range []string{"parent", "step", "revision", "raw", "scope", "generation"} {
		t.Run(field, func(t *testing.T) {
			f := h1Fresh(t)
			v := f.h1Claim(keyIntent("replace"))
			p := v.H1.Parent
			sid := h1StepID(p.Intent, 0)
			f.g.H1 = &H1Grant{RunID: f.g.BootstrapRun, Action: "step", ParentID: p.ID, StepID: sid, Revision: v.Revision, SHA256: v.SHA256}
			f.grant()
			q := f.q("begin")
			q.ExpectedRevision = v.Revision
			q.ExpectedSHA256 = v.SHA256
			q.OperationID = sid
			q.Scope = "key-add"
			q.H1 = &H1Request{ParentID: p.ID, StepID: sid}
			switch field {
			case "parent":
				q.H1.ParentID = "c:" + p.Intent.RequestID
			case "step":
				q.H1.StepID = sid + "-again"
			case "revision":
				q.ExpectedRevision--
			case "raw":
				q.ExpectedSHA256 = strings.Repeat("0", 64)
			case "scope":
				q.Scope = "initial-key"
			case "generation":
				q.Generation = "8"
			}
			if _, e := f.a.Do(q, true); e == nil {
				t.Fatal("wrong binding accepted", field)
			}
			if len(f.observe().H1.Parent.Steps) != 0 {
				t.Fatal("denial mutated parent")
			}
		})
	}
	f := h1Fresh(t)
	f.h1Claim(retryIntent("4", 0))
	q := f.h1Begin(0)
	f.write(f.changed("routing", "retry", "request-retry"))
	f.commit(q)
	raw, e := os.ReadFile(f.a.root + "/record.json")
	if e != nil {
		t.Fatal(e)
	}
	var r record
	if json.Unmarshal(raw, &r) != nil {
		t.Fatal("decode")
	}
	for _, field := range []string{"source", "generation", "raw", "receipt", "transition"} {
		var c record
		json.Unmarshal(raw, &c)
		s := &c.H1.Parent.Steps[0]
		switch field {
		case "source":
			s.SourceID = "other-source"
		case "generation":
			s.Generation = "8"
		case "raw":
			s.BeforeSHA256 = strings.Repeat("b", 64)
		case "receipt":
			s.Receipt = strings.Repeat("c", 64)
		case "transition":
			s.TransitionDigest = strings.Repeat("d", 64)
		}
		if validH1Record(&c) {
			t.Fatal("corrupt receipt accepted", field)
		}
	}
}

func TestH1ConsumeAndCommitRepliesRemainIdempotent(t *testing.T) {
	f := h1Fresh(t)
	f.h1Claim(retryIntent("4", 0))
	commit := f.h1Begin(0)
	f.write(f.changed("routing", "retry", "request-retry"))
	f.commit(commit)
	v := f.observe()
	q := f.q("consume")
	q.ExpectedRevision = v.Revision
	q.ExpectedSHA256 = v.SHA256
	q.H1 = &H1Request{ParentID: v.H1.Parent.ID, TerminalDigest: digest([]byte("original Manager terminal"))}
	consumed := f.do(q, true)
	f.do(q, true) // same consume after a lost reply, no revision bump
	commit.Action = "commit"
	commit.ExpectedSHA256 = v.SHA256
	if got := f.do(commit, true); got.Revision != consumed.Revision {
		t.Fatal("commit acknowledgement mutated consumed parent")
	}
	release := q
	release.Action = "release"
	release.ExpectedRevision = consumed.Revision
	f.do(release, true)
	f.do(q, true) // consumed anchor still acknowledges the original terminal
	f.h1Claim(retryIntent("5", 0))
	if _, e := f.a.Do(q, true); e == nil {
		t.Fatal("late consume touched new parent")
	}
}
