package proxy

import (
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Plugin pages are untrusted executable documents served from the Control
// origin. The isolation that contains them is delivered by the Manager, never
// by the plugin, so every /v0/resource/plugins/* response is rewritten to one
// opaque-origin document with no ambient authority:
//
//   - a sandbox CSP without allow-same-origin, which removes storage, DOM and
//     same-origin API access for plugin code;
//   - response headers confined to an allowlist, so a plugin cannot plant
//     origin-wide directives (Clear-Site-Data, NEL, Report-To,
//     Reporting-Endpoints, Strict-Transport-Security, Alt-Svc) or grant the
//     Control origin's credentials to another origin;
//   - no Location outside its own plugin prefix, resolved the way a browser
//     resolves it, so a redirect cannot move the sandbox into the Control
//     document or into another plugin;
//   - a fixed Access-Control-Allow-Origin without credentials so the
//     opaque-origin page can still read its own public resources.
//
// Writes are admitted here exactly like reads: a plugin page uses its own
// namespace for its own endpoints, and the containment above does not depend on
// the method.
const (
	pluginResourceSandboxCSP  = "sandbox allow-scripts allow-forms"
	pluginResourceAllowOrigin = "*"
)

// pluginResourceAllowedResponseHeaders is the complete set of upstream response
// headers a plugin resource may contribute. Everything else is dropped before
// the sandbox headers are written.
//
// The list is the representation of the document itself (type, length, encoding,
// language, disposition, validators, caching, ranges) and nothing that acts on
// the Control origin as a whole. Origin-wide directives are exactly what an
// untrusted document must not be able to set: Clear-Site-Data wipes Console
// storage and cache, NEL/Report-To/Reporting-Endpoints redirect origin-wide
// failure reports to an endpoint the plugin chooses, Strict-Transport-Security
// and Alt-Svc change how every later Console request is made.
var pluginResourceAllowedResponseHeaders = map[string]bool{
	"Content-Type":        true,
	"Content-Length":      true,
	"Content-Encoding":    true,
	"Content-Language":    true,
	"Content-Disposition": true,
	"ETag":                true,
	"Last-Modified":       true,
	"Cache-Control":       true,
	"Expires":             true,
	"Vary":                true,
	"Accept-Ranges":       true,
	"Content-Range":       true,
	// Location is carried, but only after pluginResourceRedirectAllowed has
	// confirmed it stays inside the answering plugin's own prefix. The hook
	// validates it before this rewrite runs, and a value that fails validation
	// fails the whole response with 502 rather than reaching here.
	"Location": true,
}

var errPluginResourceRedirectOutsidePrefix = errors.New("plugin resource redirect leaves its own plugin prefix")

// applyPluginResourceSandboxHeaders replaces the upstream response headers with
// the allowlisted representation and then writes the sandbox headers on top, so
// neither the plugin nor CPA can widen what the sandboxed document receives.
func applyPluginResourceSandboxHeaders(header http.Header) {
	allowed := make(http.Header, len(pluginResourceAllowedResponseHeaders))
	for name := range pluginResourceAllowedResponseHeaders {
		for _, value := range header.Values(name) {
			allowed.Add(name, value)
		}
	}
	for name := range header {
		delete(header, name)
	}
	for name, values := range allowed {
		header[name] = values
	}
	header.Set("Content-Security-Policy", pluginResourceSandboxCSP)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Access-Control-Allow-Origin", pluginResourceAllowOrigin)
	header.Del("Access-Control-Allow-Credentials")
}

// pluginResourceHeaderValueIsRepresentable reports whether a header value is
// safe to emit: ASCII without control characters. A control character cannot
// travel in an HTTP field value, and a non-ASCII byte leaves the meaning to the
// recipient's decoding.
func pluginResourceHeaderValueIsRepresentable(value string) bool {
	for index := range len(value) {
		character := value[index]
		if character < 0x20 || character == 0x7f || character > 0x7e {
			return false
		}
	}
	return true
}

// pluginResourceRequestPrefix derives the exact /v0/resource/plugins/<id>
// prefix that owns a request, or "" when the path has no plugin segment.
func pluginResourceRequestPrefix(requestPath string) string {
	cleaned := strings.TrimRight(requestPath, "/")
	if !strings.HasPrefix(cleaned, cpaPluginResourcePrefix+"/") {
		return ""
	}
	rest := strings.TrimPrefix(cleaned, cpaPluginResourcePrefix+"/")
	pluginID, _, _ := strings.Cut(rest, "/")
	if pluginID == "" {
		return ""
	}
	return cpaPluginResourcePrefix + "/" + pluginID
}

// pluginResourceRedirectAllowed reports whether a redirect target stays inside
// the plugin resource prefix that answered the request. Absolute URLs, traversal
// targets and targets whose meaning differs between this parser and a browser
// are refused: a plugin must not bounce the sandbox into the Control document,
// another plugin, or any other origin.
//
// A relative target resolves against the directory of the request path, exactly
// as the browser resolves Location, so a nested asset keeps its own redirects
// while the confinement check still runs on the resolved path.
func pluginResourceRedirectAllowed(location, requestPath, pluginPrefix string) bool {
	value := strings.TrimSpace(location)
	if value == "" || pluginPrefix == "" {
		return false
	}
	// Reject anything whose path meaning is not agreed on by every parser that
	// will see it:
	//
	//   - `\` is an ordinary path character to url.Parse but a separator to a
	//     browser, so `..\..\console` passes a leading-slash check and still
	//     resolves to /console, and `\\evil.example/x` resolves to
	//     https://evil.example/x;
	//   - control characters cannot appear in the value the browser receives.
	//
	// Refusing them is the only answer that does not depend on modelling every
	// browser's normalization.
	if strings.ContainsRune(value, '\\') || !pluginResourceHeaderValueIsRepresentable(value) {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Opaque != "" {
		return false
	}
	if parsed.Path == "" {
		return false
	}
	target := parsed.Path
	if !strings.HasPrefix(target, "/") {
		base := requestPath
		if base == "" {
			base = pluginPrefix
		}
		target = path.Dir(base) + "/" + target
	}
	if path.Clean(target) != target {
		return false
	}
	return target == pluginPrefix || strings.HasPrefix(target, pluginPrefix+"/")
}

// pluginResourceFetchMetadataAllowed is the fail-closed Fetch Metadata policy
// for plugin resources.
//
// A sandboxed plugin document runs in an opaque origin, so the browser marks
// its own subresource requests cross-site. A same-origin request for a plugin
// resource can therefore only come from a Control document (script, worker,
// style, font, object/embed, fetch), which is exactly how plugin content would
// otherwise execute with admin authority. Those are refused, and so are
// requests without Sec-Fetch-Site at all: a client that cannot prove the
// request came from a browser-controlled sandbox context never receives plugin
// content.
func pluginResourceFetchMetadataAllowed(header http.Header) bool {
	site := strings.ToLower(strings.TrimSpace(header.Get("Sec-Fetch-Site")))
	if site == "" {
		return false
	}
	if site != "same-origin" {
		// cross-site / same-site (the sandboxed plugin document itself) and
		// none (user-initiated navigation) carry no Control authority; the
		// sandbox CSP then keeps the document opaque.
		return true
	}
	switch strings.ToLower(strings.TrimSpace(header.Get("Sec-Fetch-Dest"))) {
	case "iframe", "document", "image":
		// A Control page may frame, navigate or draw a plugin resource; none of
		// these destinations executes plugin code in the Control origin.
		return true
	default:
		return false
	}
}

// IsCanonicalPluginResourceRequest reports whether a request addresses exactly
// one plugin resource under the prefix, in the shape the sandbox contract is
// written for. It is the predicate the cross-site admin exemption uses.
//
// A plain prefix test would exempt `%2f`-encoded and dot-segment paths that a
// canonical router rejects, so the exemption would cover requests the contract
// never described. The shape here matches pluginResourceRequestPrefix: a bare
// path, no encoded or doubled separator, no dot segment, and a URI whose path
// is the one parsed.
func IsCanonicalPluginResourceRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if !IsCPAPluginResourcePath(r.URL.Path) {
		return false
	}
	if r.URL.EscapedPath() != r.URL.Path || (r.URL.RawPath != "" && r.URL.RawPath != r.URL.Path) {
		return false
	}
	if path.Clean(r.URL.Path) != r.URL.Path || strings.Contains(r.URL.Path, "//") {
		return false
	}
	if r.RequestURI != "" && strings.SplitN(r.RequestURI, "?", 2)[0] != r.URL.Path {
		return false
	}
	return pluginResourceRequestPrefix(r.URL.Path) != ""
}
