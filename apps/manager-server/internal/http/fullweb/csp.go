package fullweb

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"regexp"
	"strings"
)

// The admin document is the one page in the deployment that holds Admin
// authority. It is served with a nonce-based CSP so plugin-authored content can
// never execute inside it, while a plugin page itself is confined to an
// opaque-origin sandbox document by the proxy package's plugin isolation.
//
// The policy is built from the Console's real transport: Full Web always derives
// its API base from window.location and never configures a cross-origin Manager,
// so connect-src stays 'self'. Images, styles and fonts are all bundled into
// this build; the only external hrefs are user-initiated navigations, which no
// fetch directive governs.
//
// script-src pairs a per-response nonce with 'strict-dynamic': the built entry
// and everything it imports are trusted, and no host or path allowlist can
// reintroduce plugin code. worker-src stays 'self'; a same-origin worker or
// script whose URL points into /v0/resource/plugins/ is additionally refused
// by the plugin resource Fetch Metadata policy, since CSP alone cannot exclude a
// path from 'self'.
const (
	adminDocumentStyleSources = "'self' 'unsafe-inline'"
	adminDocumentImageSources = "'self' data:"
	adminDocumentFontSources  = "'self' data:"

	// cspNoncePlaceholder is the build-time token the Full Web entry must carry
	// on every script element. Manager replaces it per response, so a served
	// document is never cacheable with a stale nonce and no script element can
	// be introduced without a nonce.
	cspNoncePlaceholder = "__CPAMP_CSP_NONCE__"

	// failClosedDocumentCSP refuses all script execution. It is served instead of
	// a nonce policy whenever the document cannot be proven to carry a nonce on
	// every script element.
	failClosedDocumentCSP = "script-src 'none'; object-src 'none'; base-uri 'none'; " +
		"frame-ancestors 'self'; form-action 'self'"
)

var (
	scriptOpenTag   = regexp.MustCompile(`(?i)<script(?:\s[^>]*)?>`)
	scriptNonceAttr = regexp.MustCompile(`(?i)\snonce="[^"]+"`)
)

// newDocumentNonce returns a fresh CSP nonce for one document response.
func newDocumentNonce() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(raw), nil
}

// documentCSP is the admin document policy.
func documentCSP(nonce string) string {
	var policy strings.Builder
	policy.WriteString("script-src 'nonce-")
	policy.WriteString(nonce)
	policy.WriteString("' 'strict-dynamic'; ")
	policy.WriteString("worker-src 'self'; ")
	policy.WriteString("object-src 'none'; ")
	policy.WriteString("base-uri 'none'; ")
	policy.WriteString("frame-src 'self'; ")
	policy.WriteString("frame-ancestors 'self'; ")
	policy.WriteString("form-action 'self'; ")
	policy.WriteString("style-src " + adminDocumentStyleSources + "; ")
	policy.WriteString("img-src " + adminDocumentImageSources + "; ")
	policy.WriteString("font-src " + adminDocumentFontSources + "; ")
	policy.WriteString("connect-src 'self'")
	return policy.String()
}

// allScriptsNonced reports whether every script element of the document carries
// a nonce attribute. A document with no script element is not trusted either:
// it means the served artifact is not the one this build produced.
func allScriptsNonced(document []byte) bool {
	matches := scriptOpenTag.FindAll(document, -1)
	if len(matches) == 0 {
		return false
	}
	for _, match := range matches {
		if !scriptNonceAttr.Match(match) {
			return false
		}
	}
	return true
}

// applyAdminDocumentCSP replaces the build-time nonce placeholder and returns
// the document together with the policy to set on the response.
//
// The failure mode is deliberate: a document whose script elements cannot all be
// proven to carry the placeholder is served untouched with a policy that forbids
// script entirely. Trusting the nonce alone would leave any nonce-less script
// free to run, which is exactly the plugin-execution bug this policy exists to
// prevent, so the document degrades to a blank admin page instead.
func applyAdminDocumentCSP(index []byte) ([]byte, string) {
	nonce, err := newDocumentNonce()
	if err != nil {
		return index, failClosedDocumentCSP
	}
	if !bytes.Contains(index, []byte(cspNoncePlaceholder)) {
		return index, failClosedDocumentCSP
	}
	stamped := bytes.ReplaceAll(index, []byte(cspNoncePlaceholder), []byte(nonce))
	if !allScriptsNonced(stamped) {
		return index, failClosedDocumentCSP
	}
	return stamped, documentCSP(nonce)
}
