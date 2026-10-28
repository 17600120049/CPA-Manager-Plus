package panel

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func panelFS(document string) fstest.MapFS {
	return fstest.MapFS{embeddedPanelPath: &fstest.MapFile{Data: []byte(document)}}
}

// The legacy admin document holds admin authority, so it must carry a nonce CSP
// that admits exactly its own scripts, with a fresh nonce per response.
func TestServeManagementHTMLCarriesNonceCSP(t *testing.T) {
	document := "<!doctype html><html><head>" +
		`<script type="module" crossorigin>var a=1;</script>` +
		"<script>window.x=1;</script>" +
		"</head><body>panel</body></html>"
	service := New("", panelFS(document))

	first := httptest.NewRecorder()
	service.ServeManagementHTML(first, httptest.NewRequest(http.MethodGet, "/management.html", nil), nil)

	policy := first.Header().Get("Content-Security-Policy")
	if policy == "" {
		t.Fatal("the legacy admin document was served without a CSP")
	}
	if !strings.HasPrefix(policy, "script-src 'nonce-") {
		t.Fatalf("policy does not start from a nonce: %q", policy)
	}
	if !strings.Contains(policy, "'strict-dynamic'") && strings.Contains(policy, "unsafe-inline") &&
		strings.Contains(policy, "script-src 'nonce-") && strings.Contains(policy, "script-src 'self'") {
		t.Fatalf("policy admits a host list: %q", policy)
	}
	if strings.Contains(policy, "script-src 'self'") || strings.Contains(policy, "script-src 'unsafe-inline'") {
		t.Fatalf("policy allows script by source: %q", policy)
	}

	body := first.Body.String()
	nonce := strings.TrimSuffix(strings.TrimPrefix(strings.SplitN(policy, "'", 3)[1], "nonce-"), "'")
	if nonce == "" {
		t.Fatalf("nonce missing from policy %q", policy)
	}
	if got := strings.Count(body, `nonce="`+nonce+`"`); got != 2 {
		t.Fatalf("stamped nonce count = %d, want 2 (one per script element): %s", got, body)
	}
	// The served document and its policy must agree: the nonce in the policy is
	// the only nonce any script element carries.
	if strings.Contains(body, "nonce=\"\"") {
		t.Fatalf("empty nonce attribute in body: %s", body)
	}

	// A second response is a fresh nonce: a served document is never cacheable
	// with a stale one.
	second := httptest.NewRecorder()
	service.ServeManagementHTML(second, httptest.NewRequest(http.MethodGet, "/management.html", nil), nil)
	secondPolicy := second.Header().Get("Content-Security-Policy")
	if secondPolicy == policy {
		t.Fatal("two responses shared one nonce")
	}
}

// A document that cannot be stamped is served with script execution refused
// rather than unconfined: the failure mode is a blank page, not an open one.
func TestServeManagementHTMLFailsClosedWithoutScriptElements(t *testing.T) {
	service := New("", panelFS("<!doctype html><html><body>no scripts here</body></html>"))
	recorder := httptest.NewRecorder()
	service.ServeManagementHTML(recorder, httptest.NewRequest(http.MethodGet, "/management.html", nil), nil)

	if got := recorder.Header().Get("Content-Security-Policy"); got != legacyPanelFailClosedCSP {
		t.Fatalf("policy = %q, want the fail-closed policy", got)
	}
	if !strings.Contains(recorder.Body.String(), "no scripts here") {
		t.Fatalf("document was not served: %q", recorder.Body.String())
	}
}

// An operator-supplied panel document gets the same treatment as the embedded
// one, and an existing nonce in the artifact is replaced rather than trusted.
func TestServeManagementHTMLStampsOperatorSuppliedPanel(t *testing.T) {
	path := t.TempDir() + "/management.html"
	document := `<!doctype html><html><body><script nonce="stale">asset()</script></body></html>`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(path, nil)

	recorder := httptest.NewRecorder()
	service.ServeManagementHTML(recorder, httptest.NewRequest(http.MethodGet, "/management.html", nil), nil)

	policy := recorder.Header().Get("Content-Security-Policy")
	if !strings.HasPrefix(policy, "script-src 'nonce-") {
		t.Fatalf("policy = %q", policy)
	}
	if strings.Contains(recorder.Body.String(), `nonce="stale"`) {
		t.Fatalf("a stale artifact nonce survived: %s", recorder.Body.String())
	}
}

// A `<script>` string inside JavaScript is program source, not a tag. Stamping
// it rewrites the program: the real panel contains
// `innerHTML=`<script><\/script>“ at offset ~110786, which only survives because
// the literal is a template string. The scan must copy element bodies verbatim.
func TestStampLegacyPanelLeavesScriptLiteralsAlone(t *testing.T) {
	document := `<!doctype html><html><body>` +
		"<script>var t=`<script><\\/script>`;var u='<script>';</script>" +
		"<script type=\"module\">x()</script>" +
		"</body></html>"

	stamped, ok := stampLegacyPanelScripts([]byte(document), "abc123")
	if !ok {
		t.Fatal("a document with script elements was reported as unstampable")
	}
	text := string(stamped)

	// Exactly the two real opening tags carry the nonce, no more.
	if got := strings.Count(text, `nonce="abc123"`); got != 2 {
		t.Fatalf("stamped nonce count = %d, want 2:\n%s", got, text)
	}
	// The literals are byte-identical to the source.
	if !strings.Contains(text, "var t=`<script><\\/script>`") {
		t.Fatalf("script literal was rewritten:\n%s", text)
	}
	if !strings.Contains(text, "var u='<script>'") {
		t.Fatalf("script literal in single quotes was rewritten:\n%s", text)
	}
}

// The built panel really does contain such a literal, so the stamper is exercised
// against it rather than only against a synthetic case.
func TestStampLegacyPanelOnBuiltPanelKeepsProgramSource(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "httpapi", "web", "management.html"))
	if err != nil {
		t.Skipf("built panel not present: %v", err)
	}
	stamped, ok := stampLegacyPanelScripts(raw, "abc123")
	if !ok {
		t.Fatal("the built panel has no script elements")
	}
	// Every occurrence of the nonce must be an attribute on a real opening tag,
	// which means the count equals the number of script elements.
	open := strings.Count(strings.ToLower(string(raw)), "<script")
	if got := strings.Count(string(stamped), `nonce="abc123"`); got != open {
		t.Fatalf("stamped %d of %d script elements", got, open)
	}
}
