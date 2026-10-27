package main

import (
	"net/http"
	"path"
	"strings"

	proxysvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proxy"
)

// supportPluginHeader mirrors the CPA contract: the header is present only
// when the Manager's saved connection can serve plugins end to end.
const supportPluginHeader = "X-CPA-SUPPORT-PLUGIN"
const supportPluginHeaderValue = "1"

// supportPluginHeaderApplies reports whether a response carries the plugin
// support guarantee.
//
// Full Web reads this header off any successful authenticated response and
// publishes it to the navigation, so it must describe the Manager's own saved
// connection rather than the caller or the specific resource. Every successful
// response on a routed Manager path therefore carries it. The exclusions are
// deliberate:
//
//   - plugin resources keep their opaque-origin sandbox contract and must not
//     advertise management capability to plugin code;
//   - the private internal namespaces are not part of any public contract;
//   - a failed response says nothing, because capability is only true when the
//     Manager can actually answer for the saved connection.
func supportPluginHeaderApplies(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	requestPath := r.URL.Path
	if strings.HasPrefix(requestPath, "/internal/") {
		return false
	}
	return !proxysvc.IsCPAPluginResourcePath(requestPath)
}

// withSupportPluginHeader tells Full Web whether plugin management and plugin
// pages are reachable on this deployment.
//
// The Manager is the only component that knows whether the saved CPA connection
// is a v8 install its proxy may serve plugins for, so it answers this on its own
// management surface instead of relying on a CPA response header that Fresh
// never forwards. A default Fresh install is therefore plugin-capable without
// any plugin-specific configuration.
//
// The header is written when the status line is committed, not before: a failed
// response must not claim the capability, because the caller reads this value as
// the deployment's answer rather than the outcome of one request.
func withSupportPluginHeader(next http.Handler) http.Handler {
	if next == nil {
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !supportPluginHeaderApplies(r) {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(&supportPluginResponseWriter{ResponseWriter: w}, r)
	})
}

// supportPluginResponseWriter adds the support header to a successful response
// only, including one that writes a body without an explicit status.
type supportPluginResponseWriter struct {
	http.ResponseWriter
	committed bool
}

func (w *supportPluginResponseWriter) WriteHeader(status int) {
	if !w.committed {
		w.committed = true
		if status < http.StatusBadRequest {
			w.Header().Set(supportPluginHeader, supportPluginHeaderValue)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *supportPluginResponseWriter) Write(body []byte) (int, error) {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

// Unwrap exposes the underlying writer to http.ResponseController so flushing,
// hijacking and deadlines keep working through the wrapper.
func (w *supportPluginResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// pluginResourceRequest reports whether a request addresses the CPA plugin
// resource namespace, which the Manager must proxy itself so every response
// carries the sandbox contract. The predicate is deliberately conservative:
// an encoded, doubled or dot-segment path never reaches the proxy as a plugin
// resource.
func pluginResourceRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if !proxysvc.IsCPAPluginResourcePath(r.URL.Path) {
		return false
	}
	if r.URL.EscapedPath() != r.URL.Path || (r.URL.RawPath != "" && r.URL.RawPath != r.URL.Path) {
		return false
	}
	if path.Clean(r.URL.Path) != r.URL.Path || strings.Contains(r.URL.Path, "//") {
		return false
	}
	// A query string is part of a plugin's own resource contract (cache-busting
	// suffixes, asset versioning), so it is preserved rather than refused. The
	// upstream receives the same request URI the caller sent.
	if r.RequestURI != "" && strings.SplitN(r.RequestURI, "?", 2)[0] != r.URL.Path {
		return false
	}
	return true
}
