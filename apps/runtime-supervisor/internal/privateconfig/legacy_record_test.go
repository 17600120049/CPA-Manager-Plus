package privateconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// legacyOperation rewrites ONE record field into the exact JSON a pre-allowlist
// Runtime wrote: the scalar lives in the legacy fields and there is no
// path/value pair.
func legacyOperation(id, scope string, retry, interval int, intervalSet bool, state string) map[string]any {
	op := map[string]any{"id": id, "scope": scope, "retry": retry, "generation": "7", "state": state}
	if interval != 0 {
		op["maxRetryInterval"] = interval
	}
	if intervalSet {
		op["maxRetryIntervalSet"] = true
	}
	return op
}

// patchRecord edits the durable record in place, modelling the bytes an upgrade
// finds on an installation that already ran the pre-allowlist writer. Values stay
// raw so device/inode and lastGood keep their exact bytes.
func (f *fixture) patchRecord(mutate func(map[string]json.RawMessage)) {
	f.t.Helper()
	name := filepath.Join(f.a.root, "record.json")
	b, err := os.ReadFile(name)
	if err != nil {
		f.t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err = json.Unmarshal(b, &record); err != nil {
		f.t.Fatal(err)
	}
	mutate(record)
	if b, err = json.Marshal(record); err != nil {
		f.t.Fatal(err)
	}
	if err = os.WriteFile(name, b, 0600); err != nil {
		f.t.Fatal(err)
	}
}

// legacyJSON renders one pre-allowlist record fragment.
func legacyJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// changedInt is the same one-leaf change a CPA PUT of that integer would make.
func (f *fixture) changedInt(value string, path ...string) []byte {
	f.t.Helper()
	b, err := os.ReadFile(f.a.config)
	if err != nil {
		f.t.Fatal(err)
	}
	node, err := tree(b)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = set(node, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: value}, path...); err != nil {
		f.t.Fatal(err)
	}
	out, err := yaml.Marshal(node)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) rebuild() {
	f.a = New(f.a.root, f.a.config, f.a.grant, f.a.namespace, 7, uint32(os.Geteuid()))
}

// TestPreAllowlistOperationScopesStayValid is the allowlist contract for the
// records an upgrade already carries: the legacy scopes and their legacy
// scalars stay acceptable, and a mixed shape never is.
func TestPreAllowlistOperationScopesStayValid(t *testing.T) {
	legacy := func(scope string, mutate func(*Operation)) record {
		r := record{Version: 1, Installed: true, Operation: Operation{ID: "legacy-operation", Scope: scope, Generation: "7", State: "persisted"}}
		mutate(&r.Operation)
		return r
	}
	field := func(path string, value any) record {
		return record{Version: 1, Installed: true, Operation: Operation{ID: "reserved-operation", Scope: path, Path: path, Value: value, Generation: "7", State: "persisted"}}
	}
	for _, tc := range []struct {
		name string
		r    record
		ok   bool
	}{
		{"legacy retry", legacy("request-retry", func(o *Operation) { o.Retry = 4 }), true},
		{"legacy retry bound", legacy("request-retry", func(o *Operation) { o.Retry = 10 }), true},
		{"legacy retry out of range", legacy("request-retry", func(o *Operation) { o.Retry = 11 }), false},
		{"legacy retry with an interval", legacy("request-retry", func(o *Operation) { o.Retry = 4; o.MaxRetryInterval = 30; o.MaxRetryIntervalSet = true }), false},
		{"legacy interval", legacy("max-retry-interval", func(o *Operation) { o.MaxRetryInterval = 30; o.MaxRetryIntervalSet = true }), true},
		{"legacy interval bound", legacy("max-retry-interval", func(o *Operation) { o.MaxRetryInterval = 300; o.MaxRetryIntervalSet = true }), true},
		{"legacy interval out of range", legacy("max-retry-interval", func(o *Operation) { o.MaxRetryInterval = 301; o.MaxRetryIntervalSet = true }), false},
		{"legacy interval without its marker", legacy("max-retry-interval", func(o *Operation) { o.MaxRetryInterval = 30 }), false},
		{"legacy interval with a retry", legacy("max-retry-interval", func(o *Operation) { o.Retry = 2; o.MaxRetryInterval = 30; o.MaxRetryIntervalSet = true }), false},
		{"allowlisted field", field("requests.streaming.keepalive-seconds", 15), true},
		{"allowlisted field out of range", field("requests.streaming.keepalive-seconds", 301), false},
		{"scope and path disagree", field("requests.streaming.keepalive-seconds", 15), false},
		{"key step", record{Version: 2, Installed: true, Operation: Operation{ID: "k:step", Scope: "key-add", Generation: "7", State: "persisted"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "scope and path disagree" {
				tc.r.Operation.Path = "requests.streaming.bootstrap-retries"
			}
			if got := validOperation(&tc.r); got != tc.ok {
				t.Fatalf("validOperation=%v want %v for %+v", got, tc.ok, tc.r.Operation)
			}
		})
	}
}

// TestLegacyUnknownIntervalOperationStillReconciles covers an operation that was
// interrupted before the upgrade: its legacy scope must still reconcile the
// file it wrote, without a second PUT.
func TestLegacyUnknownIntervalOperationStillReconciles(t *testing.T) {
	f := fresh(t)
	f.install()
	q := f.begin("interval-legacy", "routing.retry.max-retry-interval", 120)
	f.write(f.changedInt("120", "routing", "retry", "max-retry-interval"))
	f.patchRecord(func(r map[string]json.RawMessage) {
		r["operation"] = legacyJSON(t, legacyOperation(q.OperationID, "max-retry-interval", 0, 120, true, "unknown"))
	})
	f.rebuild()
	if f.a.CheckStartup() == nil {
		t.Fatal("an unresolved legacy operation allowed a start")
	}
	b, err := os.ReadFile(f.a.config)
	if err != nil {
		t.Fatal(err)
	}
	q.Action = "commit"
	q.ExpectedSHA256 = digest(b)
	v, err := f.a.Do(q, true)
	if err != nil {
		t.Fatal("legacy operation cannot be reconciled:", err)
	}
	if v.State != "persisted" || v.Operation.State != "persisted" {
		t.Fatalf("legacy operation reconciled to %q/%q", v.State, v.Operation.State)
	}
	if got := string(v.CommittedFields["routing.retry.max-retry-interval"]); got != "120" {
		t.Fatalf("committed interval is %q", got)
	}
	if err = f.a.CheckStartup(); err != nil {
		t.Fatal(err)
	}
}
