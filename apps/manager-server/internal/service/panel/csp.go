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
	// The closing tag ends an element whose body must not be rewritten.
	legacyScriptCloseTag = regexp.MustCompile(`(?i)</script\s*>`)
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
// real script element. It reports false when the document has no script element
// at all: that means the served artifact is not the built panel, so it is not
// trusted with script execution either.
//
// The scan alternates between tags and script bodies: after stamping an opening
// tag it copies everything up to the matching `</script` unchanged. A regex over
// the whole document would also match `<script>` text inside JavaScript - the
// real panel contains `innerHTML=`<script><\/script>“ at offset ~110786 - and
// stamping that rewrites the program's source, which happens to survive only
// while the literal is a template string.
func stampLegacyPanelScripts(document []byte, nonce string) ([]byte, bool) {
	stamped := make([]byte, 0, len(document)+32)
	position := 0
	stampedAny := false
	for {
		tag := legacyScriptOpenTag.FindIndex(document[position:])
		if tag == nil {
			break
		}
		open := position + tag[0]
		close := position + tag[1]
		stamped = append(stamped, document[position:open]...)
		// Drop any nonce the artifact already carries, so the value this response
		// serves is the only one that matches its policy.
		opening := legacyNonceAttr.ReplaceAll(document[open:close], nil)
		stamped = append(stamped, opening[:len(opening)-1]...)
		stamped = append(stamped, ` nonce="`+nonce+`">`...)
		stampedAny = true

		// Copy the element's body verbatim up to its closing tag.
		body := legacyScriptCloseTag.FindIndex(document[close:])
		if body == nil {
			// An opening tag with no close: copy the rest untouched and stop, so a
			// truncated document is served as it is rather than half-rewritten.
			stamped = append(stamped, document[close:]...)
			position = len(document)
			break
		}
		end := close + body[1]
		stamped = append(stamped, document[close:end]...)
		position = end
	}
	stamped = append(stamped, document[position:]...)
	return stamped, stampedAny
}
