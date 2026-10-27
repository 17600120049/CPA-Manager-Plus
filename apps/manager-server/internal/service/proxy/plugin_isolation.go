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
//   - no Set-Cookie, so a plugin cannot plant or observe Control credentials;
//   - no Location outside its own plugin prefix, so a redirect cannot move the
//     sandbox into the Control document or into another plugin;
//   - a fixed Access-Control-Allow-Origin without credentials so the
//     opaque-origin page can still read its own public resources.
const (
	pluginResourceSandboxCSP  = "sandbox allow-scripts allow-forms"
	pluginResourceAllowOrigin = "*"
)

var errPluginResourceRedirectOutsidePrefix = errors.New("plugin resource redirect leaves its own plugin prefix")

// applyPluginResourceSandboxHeaders enforces the non-negotiable response
// headers on a plugin resource response, overriding anything the plugin sent.
func applyPluginResourceSandboxHeaders(header http.Header) {
	header.Set("Content-Security-Policy", pluginResourceSandboxCSP)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Access-Control-Allow-Origin", pluginResourceAllowOrigin)
	header.Del("Set-Cookie")
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
// the plugin resource prefix that answered the request. Absolute URLs and
// traversal targets are refused: a plugin must not bounce the sandbox into the
// Control document, another plugin, or any other origin.
//
// A relative target resolves against the directory of the request path, exactly
// as the browser resolves Location, so a nested asset keeps its own redirects
// while the confinement check still runs on the resolved path.
func pluginResourceRedirectAllowed(location, requestPath, pluginPrefix string) bool {
	value := strings.TrimSpace(location)
	if value == "" || pluginPrefix == "" {
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
