package panel

import (
	"crypto/rand"
	"encoding/base64"
	"regexp"
	"strings"
)

// The legacy /management.html page is the Foundation-era admin document. It is
// the same kind of surface as the Full Web Console document: it holds admin
// authority, so plugin-authored content must never execute inside it.
//
// The page is a single prebuilt document whose scripts are inline, so a nonce is
// the only policy that both admits the page's own bundle and excludes everything
// else. Build-time stamping is not available here (the artifact may also be
// supplied through PANEL_PATH instead of being embedded), so the nonce is
// injected per response into a copy of the document. A document whose script
// elements cannot all be stamped is served with `script-src 'none'` instead.
const (
	legacyPanelStyleSources = "'self' 'unsafe-inline'"
	legacyPanelImageSources = "'self' data:"
	legacyPanelFontSources  = "'self' data:"
)

// legacyPanelFailClosedCSP refuses all script execution. It is served instead of
// a nonce policy whenever the document cannot be proven to carry a nonce on
// every script element.
const legacyPanelFailClosedCSP = "script-src 'none'; object-src 'none'; base-uri 'none'; " +
	"frame-ancestors 'self'; form-action 'self'"

var (
	legacyScriptOpenTag = regexp.MustCompile(`(?is)<script(\s[^>]*)?>`)
	legacyNonceAttr     = regexp.MustCompile(`(?i)\snonce="[^"]*"`)
)

func legacyPanelNonce() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(raw), nil
}

// legacyPanelCSP is the policy for the legacy admin document.
func legacyPanelCSP(nonce string) string {
	var policy strings.Builder
	policy.WriteString("script-src 'nonce-")
	policy.WriteString(nonce)
	policy.WriteString("'; ")
	policy.WriteString("object-src 'none'; ")
	policy.WriteString("base-uri 'none'; ")
	policy.WriteString("frame-src 'self'; ")
	policy.WriteString("frame-ancestors 'self'; ")
	policy.WriteString("form-action 'self'; ")
	policy.WriteString("style-src " + legacyPanelStyleSources + "; ")
	policy.WriteString("img-src " + legacyPanelImageSources + "; ")
	policy.WriteString("font-src " + legacyPanelFontSources + "; ")
	policy.WriteString("connect-src 'self'")
	return policy.String()
}

// applyLegacyPanelCSP stamps a fresh nonce onto every script element and returns
// the document together with the policy to set on the response. A document that
// cannot be stamped is returned untouched with the fail-closed policy, so the
// failure mode is a blank page rather than unconfined script.
func applyLegacyPanelCSP(document []byte) ([]byte, string) {
	nonce, err := legacyPanelNonce()
	if err != nil {
		return document, legacyPanelFailClosedCSP
	}
	stamped, ok := stampLegacyPanelScripts(document, nonce)
	if !ok {
		return document, legacyPanelFailClosedCSP
	}
	return stamped, legacyPanelCSP(nonce)
}

// stampLegacyPanelScripts returns a copy of the document with the nonce on every
// script element. It reports false when the document has no script element at
// all: that means the served artifact is not the built panel, so it is not
// trusted with script execution either.
func stampLegacyPanelScripts(document []byte, nonce string) ([]byte, bool) {
	matches := legacyScriptOpenTag.FindAllIndex(document, -1)
	if len(matches) == 0 {
		return document, false
	}
	stamped := make([]byte, 0, len(document)+len(matches)*(len(nonce)+9))
	last := 0
	for _, match := range matches {
		stamped = append(stamped, document[last:match[0]]...)
		// Drop any nonce the artifact already carries, so the value this response
		// serves is the only one that matches its policy.
		tag := legacyNonceAttr.ReplaceAll(document[match[0]:match[1]], nil)
		stamped = append(stamped, tag[:len(tag)-1]...)
		stamped = append(stamped, ` nonce="`+nonce+`">`...)
		last = match[1]
	}
	stamped = append(stamped, document[last:]...)
	return stamped, true
}
