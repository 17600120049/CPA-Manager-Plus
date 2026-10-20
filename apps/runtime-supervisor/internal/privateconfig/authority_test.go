package privateconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type fixture struct {
	a    *Authority
	g    Grant
	t    *testing.T
	seed []byte
}

func fresh(t *testing.T) *fixture {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	seed := []byte("config-version: 8\nmanagement: {secret-key: 'host-secret-hash'}\naccess: {api-keys: []}\nplugins: {enabled: false, configs: {opaque: {absent: null, empty: {}, list: [], disabled: false, number: 9007199254740993}}}\n")
	config := filepath.Join(root, "config.yaml")
	if e = os.WriteFile(config, seed, 0600); e != nil {
		t.Fatal(e)
	}
	ns := filepath.Join(root, "namespace")
	if e = os.Symlink("net:[test-private]", ns); e != nil {
		t.Fatal(e)
	}
	info, _ := os.Stat(config)
	dev, ino, _ := fileID(info)
	g := Grant{SourceID: "fresh-test-source-unique", Generation: "7", Namespace: "net:[test-private]", Device: dev, Inode: ino, SeedSHA256: digest(seed),
		IssuedAtMS: time.Now().Add(-time.Second).UnixMilli(), ExpiresAtMS: time.Now().Add(time.Minute).UnixMilli()}
	f := &fixture{a: New(filepath.Join(root, "authority"), config, filepath.Join(root, "source.json"), ns, 7, uint32(os.Geteuid())), g: g, t: t, seed: seed}
	f.grant()
	if f.a.CheckStartup() == nil {
		t.Fatal("existing unadmitted YAML was trusted")
	}
	q := f.q("admit")
	q.ExpectedSHA256 = digest(seed)
	f.do(q, false)
	return f
}
func (f *fixture) grant() {
	f.t.Helper()
	b, _ := json.Marshal(f.g)
	if e := os.WriteFile(f.a.grant, b, 0600); e != nil {
		f.t.Fatal(e)
	}
}
func (f *fixture) q(action string) Request {
	return Request{Action: action, SourceID: f.g.SourceID, Generation: f.g.Generation}
}
func (f *fixture) do(q Request, running bool) Result {
	f.t.Helper()
	v, e := f.a.Do(q, running)
	if e != nil {
		f.t.Fatalf("%s: %v", q.Action, e)
	}
	return v
}
func (f *fixture) observe() Result { return f.do(f.q("observe"), false) }
func (f *fixture) begin(id, scope string, retry int) Request {
	f.t.Helper()
	v := f.observe()
	q := f.q("begin")
	q.ExpectedRevision = v.Revision
	q.ExpectedSHA256 = v.SHA256
	q.OperationID = id
	q.Scope = scope
	q.Retry = retry
	if scope == "initial-key" {
		q.KeySHA256 = digest([]byte("test-client-secret"))
	}
	v = f.do(q, true)
	if !v.Created {
		f.t.Fatal("no write reservation")
	}
	q.ExpectedRevision = v.Revision
	return q
}
func (f *fixture) write(b []byte) {
	f.t.Helper()
	if e := os.WriteFile(f.a.config, b, 0600); e != nil {
		f.t.Fatal(e)
	}
}
func (f *fixture) changed(path ...string) []byte {
	f.t.Helper()
	b, _ := os.ReadFile(f.a.config)
	n, e := tree(b)
	if e != nil {
		f.t.Fatal(e)
	}
	v := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "0"}
	if path[0] == "access" {
		v = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "test-client-secret"}}}
	}
	if e = set(n, v, path...); e != nil {
		f.t.Fatal(e)
	}
	out, e := yaml.Marshal(n)
	if e != nil {
		f.t.Fatal(e)
	}
	return out
}
func (f *fixture) commit(q Request) {
	f.t.Helper()
	b, _ := os.ReadFile(f.a.config)
	q.Action = "commit"
	q.ExpectedSHA256 = digest(b)
	v := f.do(q, true)
	if v.State != "persisted" {
		f.t.Fatal(v.State)
	}
}
func (f *fixture) install() {
	q := f.begin("install", "initial-key", 0)
	f.write(f.changed("access", "api-keys"))
	f.commit(q)
}

func TestSourceInstallRetryAndRestart(t *testing.T) {
	f := fresh(t)
	if e := f.a.CheckStartup(); e != nil {
		t.Fatal(e)
	}
	f.install()
	if e := f.a.CheckStartup(); e != nil {
		t.Fatal("installed key cannot restart:", e)
	}
	q := f.begin("retry-zero", "request-retry", 0)
	f.write(f.changed("routing", "retry", "request-retry"))
	f.commit(q)
	original := f.observe()
	rebuilt := New(f.a.root, f.a.config, f.a.grant, f.a.namespace, 7, uint32(os.Geteuid()))
	if e := rebuilt.CheckStartup(); e != nil {
		t.Fatal("committed scalar cannot restart:", e)
	}
	got, e := rebuilt.Do(f.q("observe"), false)
	if e != nil || got.SHA256 != original.SHA256 || got.Operation.ID != q.OperationID {
		t.Fatal("lost durable result", e)
	}
	// A response lost after commit is reconciled by read, never a second Begin.
	q.Action = "begin"
	q.ExpectedRevision = got.Revision
	q.ExpectedSHA256 = got.SHA256
	if _, e := rebuilt.Do(q, true); e == nil {
		t.Fatal("replayed write was authorized")
	}
}
func TestPendingWriteExcludesEveryOtherWriter(t *testing.T) {
	f := fresh(t)
	v := f.observe()
	var created atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := f.q("begin")
			q.ExpectedRevision = v.Revision
			q.ExpectedSHA256 = v.SHA256
			q.Scope = "initial-key"
			q.OperationID = strings.Repeat("x", i+1)
			q.KeySHA256 = digest([]byte("test-client-secret"))
			got, e := f.a.Do(q, true)
			if e == nil && got.Created {
				created.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatal("write reservations", created.Load())
	}
	if f.a.CheckStartup() == nil {
		t.Fatal("unresolved intent allowed startup")
	}
	q := f.q("begin")
	q.Scope = "request-retry"
	q.OperationID = "other"
	q.ExpectedRevision = f.observe().Revision
	if _, e := f.a.Do(q, true); e == nil {
		t.Fatal("cross-domain concurrent write")
	}
}
func TestLostResponseReconcilesButDoesNotReplay(t *testing.T) {
	for _, point := range []string{"before-put", "partial-put", "after-put"} {
		t.Run(point, func(t *testing.T) {
			f := fresh(t)
			q := f.begin("original-operation", "initial-key", 0)
			if point == "partial-put" {
				f.write([]byte("access: ["))
			}
			if point == "after-put" {
				f.write(f.changed("access", "api-keys"))
			}
			f.a = New(f.a.root, f.a.config, f.a.grant, f.a.namespace, 7, uint32(os.Geteuid()))
			if f.a.CheckStartup() == nil {
				t.Fatal("missing terminal receipt allowed start")
			}
			b, _ := os.ReadFile(f.a.config)
			q.Action = "commit"
			q.ExpectedSHA256 = digest(b)
			_, e := f.a.Do(q, true)
			if (point == "after-put") != (e == nil) {
				t.Fatal("wrong reconciliation", point, e)
			}
		})
	}
}
func TestRecoveryRequiresStoppedWriterOriginalLeaseAndDamagedFingerprint(t *testing.T) {
	f := fresh(t)
	f.install()
	prior := f.begin("committed-retry", "request-retry", 0)
	f.write(f.changed("routing", "retry", "request-retry"))
	f.commit(prior)
	q := f.begin("retry-interrupted", "request-retry", 1)
	f.write([]byte("routing: ["))
	q.Action = "recover"
	q.ExpectedSHA256 = digest([]byte("routing: ["))
	if _, e := f.a.Do(q, false); e == nil {
		t.Fatal("no explicit Host recovery permit")
	}
	f.g.RecoveryOperation = q.OperationID
	f.g.RecoverySHA256 = q.ExpectedSHA256
	f.grant()
	if _, e := f.a.Do(q, true); e == nil {
		t.Fatal("restored while writer was running")
	}
	f.write([]byte("different unknown data"))
	if _, e := f.a.Do(q, false); e == nil {
		t.Fatal("blindly overwrote changed file")
	}
	f.write([]byte("routing: ["))
	v := f.do(q, false)
	if v.State != "persisted" || v.Operation.State != "recovered" || !v.Installed {
		t.Fatal("recovery lost installed state")
	}
	if v.CommittedRetry == nil || *v.CommittedRetry != 0 {
		t.Fatal("failed operation replaced the committed retry allowance")
	}
	if e := f.a.CheckStartup(); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(f.a.config)
	if !strings.Contains(string(b), "test-client-secret") {
		t.Fatal("recovery deleted installed key")
	}
}

func TestUnknownOperationAfterSupervisorRestartNeedsExplicitStoppedReconciliation(t *testing.T) {
	f := fresh(t)
	q := f.begin("original-install", "initial-key", 0)
	f.write(f.changed("access", "api-keys"))
	f.a = New(f.a.root, f.a.config, f.a.grant, f.a.namespace, 8, uint32(os.Geteuid()))
	f.g.Generation = "8"
	f.grant()
	q.Generation = "8"
	q.Action = "commit"
	b, _ := os.ReadFile(f.a.config)
	q.ExpectedSHA256 = digest(b)
	if _, e := f.a.Do(q, false); e == nil {
		t.Fatal("implicit generation rebind")
	}
	f.g.RecoveryOperation = q.OperationID
	f.g.RecoverySHA256 = q.ExpectedSHA256
	f.grant()
	if _, e := f.a.Do(q, true); e == nil {
		t.Fatal("cross-generation live writer")
	}
	v := f.do(q, false)
	if !v.Installed || v.Operation.Generation != "7" || v.Generation != "8" {
		t.Fatal("lost original operation identity")
	}
	if e := f.a.CheckStartup(); e != nil {
		t.Fatal("reconciled bytes cannot restart", e)
	}
}

func TestCorruptionBetweenCommittedOperationsRequiresHostRecovery(t *testing.T) {
	f := fresh(t)
	f.install()
	last := f.observe()
	f.write([]byte("corrupted between operations"))
	if f.a.CheckStartup() == nil {
		t.Fatal("corruption started")
	}
	q := f.q("recover")
	q.OperationID = last.Operation.ID
	q.ExpectedRevision = last.Revision
	q.ExpectedSHA256 = digest([]byte("corrupted between operations"))
	if _, e := f.a.Do(q, false); e == nil {
		t.Fatal("recovered without Host decision")
	}
	f.g.RecoveryOperation = q.OperationID
	f.g.RecoverySHA256 = q.ExpectedSHA256
	f.grant()
	if _, e := f.a.Do(q, true); e == nil {
		t.Fatal("live corruption restore")
	}
	v := f.do(q, false)
	if v.Revision != last.Revision+2 || v.SHA256 != last.SHA256 || !v.Installed || v.State != "persisted" {
		t.Fatal("recovery lost its durable anchor")
	}
	if e := f.a.CheckStartup(); e != nil {
		t.Fatal(e)
	}
}
func TestUnknownFileEvidenceAndIdentityAreNeverAdopted(t *testing.T) {
	for _, kind := range []string{"foreign-source", "generation", "expired", "namespace", "replaced-inode", "hardlink", "symlink", "missing-record", "damaged-record", "lost-last-good", "invalid-mode"} {
		t.Run(kind, func(t *testing.T) {
			f := fresh(t)
			f.install()
			switch kind {
			case "foreign-source":
				f.g.SourceID = "another-foreign-source"
				f.grant()
			case "generation":
				f.a.generation = "8"
			case "expired":
				f.g.ExpiresAtMS = time.Now().Add(-time.Millisecond).UnixMilli()
				f.grant()
			case "namespace":
				f.g.Namespace = "net:[other]"
				f.grant()
			case "replaced-inode":
				os.Remove(f.a.config)
				f.write(f.seed)
			case "hardlink":
				if e := os.Link(f.a.config, f.a.config+".alias"); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				os.Rename(f.a.config, f.a.config+".old")
				os.Symlink(f.a.config+".old", f.a.config)
			case "missing-record":
				os.Remove(filepath.Join(f.a.root, "record.json"))
			case "damaged-record":
				os.WriteFile(filepath.Join(f.a.root, "record.json"), []byte("{"), 0600)
			case "lost-last-good":
				b, _ := os.ReadFile(filepath.Join(f.a.root, "record.json"))
				var r record
				json.Unmarshal(b, &r)
				r.LastGood = []byte("different")
				b, _ = json.Marshal(r)
				os.WriteFile(filepath.Join(f.a.root, "record.json"), b, 0600)
			case "invalid-mode":
				os.Chmod(f.a.config, 0666)
			}
			if f.a.CheckStartup() == nil {
				t.Fatal("untrusted state started")
			}
			if _, e := f.a.Do(f.q("begin"), true); e == nil {
				t.Fatal("untrusted state wrote")
			}
		})
	}
}
func TestFullFileValidationPreservesSecretsOpaqueDataAndPresence(t *testing.T) {
	for _, drift := range []string{"secret", "opaque", "null", "second-key", "other-root"} {
		t.Run(drift, func(t *testing.T) {
			f := fresh(t)
			q := f.begin("one-key", "initial-key", 0)
			b := f.changed("access", "api-keys")
			switch drift {
			case "secret":
				b = []byte(strings.Replace(string(b), "host-secret-hash", "changed-secret", 1))
			case "opaque":
				b = []byte(strings.Replace(string(b), "9007199254740993", "9007199254740992", 1))
			case "null":
				b = []byte(strings.Replace(string(b), "absent: null", "absent: false", 1))
			case "second-key":
				n, err := tree(b)
				if err != nil {
					t.Fatal(err)
				}
				keys := leaf(n, "access", "api-keys")
				keys.Content = append(keys.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "other"})
				b, err = yaml.Marshal(n)
				if err != nil {
					t.Fatal(err)
				}
			case "other-root":
				b = append(b, []byte("unexpected: true\n")...)
			}
			f.write(b)
			q.Action = "commit"
			q.ExpectedSHA256 = digest(b)
			if _, e := f.a.Do(q, true); e == nil {
				t.Fatal("cross-scope change committed")
			}
			if f.a.CheckStartup() == nil {
				t.Fatal("unsafe write allowed restart")
			}
		})
	}
}
