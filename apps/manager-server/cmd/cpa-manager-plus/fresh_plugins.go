package main

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	proxysvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proxy"
)

// supportPluginHeader mirrors the CPA contract: the header is present only
// when the Manager's saved connection can serve plugins end to end.
const supportPluginHeader = "X-CPA-SUPPORT-PLUGIN"
const supportPluginHeaderValue = "1"

// pluginCapabilityTTL bounds how long one proven answer serves the navigation.
// Long enough that a page load issues one CPA round trip rather than several,
// short enough that a connection change is reflected within a page refresh.
const pluginCapabilityTTL = 30 * time.Second

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
// The header is a claim about the saved connection, so it is only written when
// the connection actually proves plugin support - not merely because a route
// exists. A deployment whose connection cannot serve plugins must not advertise
// the capability, or Console renders plugin management for a CPA that has none.
//
// The header is written when the status line is committed, not before: a failed
// response must not claim the capability, because the caller reads this value as
// the deployment's answer rather than the outcome of one request.
func withSupportPluginHeader(next http.Handler, capable func(*http.Request) bool) http.Handler {
	if next == nil {
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !supportPluginHeaderApplies(r) || capable == nil || !capable(r) {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(&supportPluginResponseWriter{ResponseWriter: w}, r)
	})
}

// supportPluginCapable is the production capability answer: this installation's
// saved connection proves plugin support, and only then does the header appear.
func (a *freshApplication) supportPluginCapable(r *http.Request) bool {
	if a.pluginCapability == nil {
		return false
	}
	ctx := context.Background()
	if r != nil {
		ctx = r.Context()
	}
	return a.pluginCapability.capable(ctx)
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
// carries the sandbox contract. The shapes are the proxy package's canonical
// predicate, so the routing decision, the proxy admission and the cross-site
// exemption cannot drift apart.
func pluginResourceRequest(r *http.Request) bool {
	return proxysvc.IsCanonicalPluginResourceRequest(r)
}

// pluginCapabilityProbe proves whether a saved connection can serve plugin
// resources end to end. It answers with the CPA's own configuration document:
// a v8 build exposes the plugin section there, and a connection that cannot
// produce it cannot serve a plugin page either.
//
// The probe is not free, so it is cached for a short window and deduplicated;
// the navigation needs a stable answer across the several reads one page load
// performs, and a probe per read would put a CPA round trip behind every
// Console request.
type pluginCapabilityProbe struct {
	mu      sync.Mutex
	ttl     time.Duration
	at      time.Time
	proven  bool
	probe   func(context.Context) bool
	lastErr error
}

func newPluginCapabilityProbe(ttl time.Duration, probe func(context.Context) bool) *pluginCapabilityProbe {
	return &pluginCapabilityProbe{ttl: ttl, probe: probe}
}

// capable reports whether plugins are proven reachable on the saved connection.
// A probe that has not answered yet is not proof, so the default is false: the
// navigation must not advertise a capability the Manager cannot demonstrate.
func (p *pluginCapabilityProbe) capable(ctx context.Context) bool {
	if p == nil || p.probe == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.at.IsZero() && time.Since(p.at) < p.ttl {
		return p.proven
	}
	p.proven = p.probe(ctx)
	p.at = time.Now()
	return p.proven
}

// forget drops a cached answer so the next read proves again. A profile change
// (managed install committed, connection edited) must not inherit the previous
// connection's answer.
func (p *pluginCapabilityProbe) forget() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.at = time.Time{}
	p.proven = false
}

// v8PluginCapable proves plugin support by reading the saved CPA's configuration
// document and requiring the plugin section in it. That is the same document the
// plugin switch writes, so a connection that cannot produce it cannot serve the
// switch either.
func (a *freshApplication) v8PluginCapable(ctx context.Context) bool {
	if a.gatewayConfig == nil {
		return false
	}
	return a.gatewayConfig.SupportsPlugins(ctx)
}
