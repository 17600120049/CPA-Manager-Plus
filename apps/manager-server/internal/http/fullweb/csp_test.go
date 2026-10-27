package fullweb

import (
	"bytes"
	"strings"
	"testing"
)

func TestApplyAdminDocumentCSPStampsEveryScript(t *testing.T) {
	index := []byte(`<!doctype html><html><head>` +
		`<script id="cpamp-routing-context" type="application/json" nonce="` + cspNoncePlaceholder + `">{}</script>` +
		`</head><body><div id="root"></div>` +
		`<script type="module" src="./assets/main-abc12345.js" nonce="` + cspNoncePlaceholder + `"></script>` +
		`</body></html>`)

	stamped, policy := applyAdminDocumentCSP(index)
	body := string(stamped)

	if bytes.Contains(stamped, []byte(cspNoncePlaceholder)) {
		t.Fatal("placeholder survived stamping")
	}
	// Both the executable entry and the inert JSON carrier must be stamped: the
	// validation below refuses a document where any script element lacks a nonce.
	if got := strings.Count(body, `nonce="`); got != 2 {
		t.Fatalf("nonce attributes = %d, want 2", got)
	}
	if !strings.Contains(policy, "script-src 'nonce-") || !strings.Contains(policy, "'strict-dynamic'") {
		t.Fatalf("policy is not nonce+strict-dynamic: %q", policy)
	}
	// The nonce in the policy must be the one written into the document, or no
	// script would run and the admin UI would silently break.
	nonce := policy[strings.Index(policy, "'nonce-")+len("'nonce-"):]
	nonce = nonce[:strings.IndexByte(nonce, '\'')]
	if nonce == "" || !strings.Contains(body, `nonce="`+nonce+`"`) {
		t.Fatalf("policy nonce %q absent from document", nonce)
	}
	for _, directive := range []string{"worker-src 'self'", "object-src 'none'", "base-uri 'none'", "frame-src 'self'", "frame-ancestors 'self'", "form-action 'self'", "connect-src 'self'"} {
		if !strings.Contains(policy, directive) {
			t.Fatalf("policy missing %q: %s", directive, policy)
		}
	}
}

func TestApplyAdminDocumentCSPFailsClosed(t *testing.T) {
	cases := []struct {
		name  string
		index string
	}{
		{
			// The build did not inject the token: nothing can be trusted.
			name:  "placeholder absent",
			index: `<html><body><script type="module" src="./assets/main-abc12345.js"></script></body></html>`,
		},
		{
			// One stamped script and one unstamped script. Serving the nonce policy
			// here would leave the unstamped script free to execute, which is the
			// exact confusion this policy exists to prevent.
			name: "one script without nonce",
			index: `<html><body><script nonce="` + cspNoncePlaceholder + `" src="./assets/a.js"></script>` +
				`<script src="./assets/b.js"></script></body></html>`,
		},
		{
			// No script element at all means this is not the artifact the build
			// produced; trusting it would silently serve an unknown document.
			name:  "no script element",
			index: `<html><body><div id="root"></div></body></html>`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			stamped, policy := applyAdminDocumentCSP([]byte(testCase.index))
			if policy != failClosedDocumentCSP {
				t.Fatalf("policy = %q, want fail-closed", policy)
			}
			if !strings.Contains(policy, "script-src 'none'") {
				t.Fatalf("fail-closed policy permits script: %q", policy)
			}
			// The body must be untouched: a partially stamped document served with
			// script-src 'none' is inert, while rewriting it would hide the defect.
			if string(stamped) != testCase.index {
				t.Fatal("fail-closed path modified the document")
			}
		})
	}
}

func TestAdminDocumentNonceIsUniquePerResponse(t *testing.T) {
	index := []byte(`<script nonce="` + cspNoncePlaceholder + `"></script>`)
	seen := map[string]bool{}
	for range 32 {
		_, policy := applyAdminDocumentCSP(index)
		if seen[policy] {
			t.Fatal("nonce reused across responses")
		}
		seen[policy] = true
	}
}
