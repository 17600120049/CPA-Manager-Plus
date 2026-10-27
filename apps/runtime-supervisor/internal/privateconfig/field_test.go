package privateconfig

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// changedLeaf rewrites exactly one YAML scalar, creating missing sections.
func (f *fixture) changedLeaf(tag, text string, path ...string) []byte {
	f.t.Helper()
	b, _ := os.ReadFile(f.a.config)
	n, e := tree(b)
	if e != nil {
		f.t.Fatal(e)
	}
	if e = set(n, &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: text}, path...); e != nil {
		f.t.Fatal(e)
	}
	out, e := yaml.Marshal(n)
	if e != nil {
		f.t.Fatal(e)
	}
	return out
}

func TestH1FieldScopeBoundariesAndCrossFieldRestart(t *testing.T) {
	f := h1Fresh(t)
	keepalive := fieldIntent("6", "requests.streaming.keepalive-seconds", 0)
	if !keepalive.valid() || keepalive.scopes()[0] != "requests.streaming.keepalive-seconds" {
		t.Fatal("field identity rejected")
	}
	for _, n := range []any{-1, 301, 1.5, "60", true} {
		bad := keepalive
		bad.Value = n
		if bad.valid() {
			t.Fatal("out-of-range or wrong-typed H1 grant accepted", n)
		}
	}
	unknown := keepalive
	unknown.Path = "requests.proxy-url"
	if unknown.valid() {
		t.Fatal("non-allowlisted H1 grant accepted")
	}
	f.h1Claim(keepalive)
	q := f.h1Begin(0)
	if q.Scope != "requests.streaming.keepalive-seconds" {
		t.Fatal("scope drift", q.Scope)
	}
	before, err := os.ReadFile(f.a.config)
	if err != nil {
		t.Fatal(err)
	}
	after := f.changedLeaf("!!int", "0", "requests", "streaming", "keepalive-seconds")
	if !validChange(before, after, intentOperation(keepalive, q.OperationID, q.Scope, f.g.Generation)) {
		t.Fatal("field scalar was not a valid isolated change")
	}
	if validChange(before, after, intentOperation(fieldIntent("6", "requests.passthrough-headers", false), q.OperationID, q.Scope, f.g.Generation)) {
		t.Fatal("field write accepted as another field")
	}
	f.write(after)
	f.commit(q)
	f.h1Finish()
	v := f.observe()
	if raw := v.CommittedFields["requests.streaming.keepalive-seconds"]; string(raw) != "0" {
		t.Fatal("committed field not recorded", v.CommittedFields)
	}
	if _, ok := v.CommittedFields["requests.passthrough-headers"]; ok {
		t.Fatal("field write crossed into another slot")
	}
	headers := fieldIntent("7", "requests.passthrough-headers", true)
	f.h1Claim(headers)
	q = f.h1Begin(0)
	f.write(f.changedLeaf("!!bool", "true", "requests", "passthrough-headers"))
	f.commit(q)
	f.h1Finish()
	v = f.observe()
	if string(v.CommittedFields["requests.streaming.keepalive-seconds"]) != "0" || string(v.CommittedFields["requests.passthrough-headers"]) != "true" {
		t.Fatal("per-field committed slots crossed", v.CommittedFields)
	}
	restarted := New(f.a.root, f.a.config, f.a.grant, f.a.namespace, 7, uint32(os.Geteuid()))
	f.a = restarted
	v = f.observe()
	if string(v.CommittedFields["requests.passthrough-headers"]) != "true" || string(v.CommittedFields["requests.streaming.keepalive-seconds"]) != "0" {
		t.Fatal("committed fields lost across reload", v.CommittedFields)
	}
	if f.a.CheckStartup() == nil {
		t.Fatal("old claim was incorrectly valid for restart")
	}
}

func TestFieldRejectsOpaqueDrift(t *testing.T) {
	good := []byte("requests:\n  passthrough-headers: false\n")
	op := intentOperation(fieldIntent("8", "requests.passthrough-headers", true), "op", "requests.passthrough-headers", "7")
	if op.Path != "requests.passthrough-headers" || op.Value != true {
		t.Fatal("operation not generic", op)
	}
	if !validChange(good, []byte("requests:\n  passthrough-headers: true\n"), op) {
		t.Fatal("single boolean change rejected")
	}
	if validChange(good, []byte("requests:\n  passthrough-headers: 1\n"), op) {
		t.Fatal("wrong YAML type accepted")
	}
	if validChange(good, []byte("requests:\n  passthrough-headers: true\n  unknown: 1\n"), op) {
		t.Fatal("unrelated sibling drift permitted")
	}
	if validChange(good, []byte("requests:\n  streaming:\n    keepalive-seconds: 0\n  passthrough-headers: true\n"), op) {
		t.Fatal("second field drift permitted")
	}
	number := []byte("requests:\n  streaming:\n    keepalive-seconds: 30\n")
	numberOp := intentOperation(fieldIntent("9", "requests.streaming.keepalive-seconds", 30), "op", "requests.streaming.keepalive-seconds", "7")
	if !validChange([]byte("requests:\n  streaming:\n    keepalive-seconds: 0\n"), number, numberOp) {
		t.Fatal("numeric change rejected")
	}
	if validChange([]byte("requests:\n  streaming:\n    keepalive-seconds: 0\n"), []byte("requests:\n  streaming:\n    keepalive-seconds: 1.5\n"), numberOp) {
		t.Fatal("non-integer accepted")
	}
}

func TestFieldCreateMissingSection(t *testing.T) {
	// The shipped seed has no requests section at all: a first write must create
	// the path, and the reserved scalar must be exactly what CPA will read.
	before := []byte("routing:\n  retry:\n    request-retry: 2\n")
	after := []byte("routing:\n  retry:\n    request-retry: 2\nrequests:\n  streaming:\n    keepalive-seconds: 60\n")
	op := intentOperation(fieldIntent("a", "requests.streaming.keepalive-seconds", 60), "op", "requests.streaming.keepalive-seconds", "7")
	if !validChange(before, after, op) {
		t.Fatal("missing section could not be created")
	}
}

func TestKeyIntentDoesNotCarryFieldMaterial(t *testing.T) {
	for _, mode := range []string{"replace", "revoke"} {
		key := keyIntent(mode)
		if !key.valid() {
			t.Fatal("existing Key intent rejected", mode)
		}
		key.Path = "requests.passthrough-headers"
		key.Value = true
		if key.valid() {
			t.Fatal("Key intent admitted field material", mode)
		}
	}
}

func TestFieldAllowlistCoversManagerTable(t *testing.T) {
	// The two independent modules must agree on the writable set, or the Manager
	// would offer a field the Host refuses (or the reverse).
	paths := []string{
		"routing.retry.request-retry",
		"routing.retry.max-retry-interval",
		"requests.streaming.keepalive-seconds",
		"requests.streaming.bootstrap-retries",
		"requests.nonstream-keepalive-interval",
		"requests.passthrough-headers",
	}
	if len(configFields) != len(paths) {
		t.Fatal("allowlist size drift", len(configFields))
	}
	for _, path := range paths {
		parts, spec, ok := fieldFor(path)
		if !ok || len(parts) < 2 || (spec.kind != "int" && spec.kind != "bool") {
			t.Fatal("field missing", path)
		}
	}
	if !validFieldValue("requests.nonstream-keepalive-interval", 300) || validFieldValue("requests.nonstream-keepalive-interval", 301) {
		t.Fatal("interval bounds drift")
	}
	if !validFieldValue("requests.streaming.bootstrap-retries", 10) || validFieldValue("requests.streaming.bootstrap-retries", 11) {
		t.Fatal("retry bounds drift")
	}
}
