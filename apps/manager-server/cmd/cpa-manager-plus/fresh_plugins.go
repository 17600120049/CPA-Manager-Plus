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

// pluginCapabilityProbeTimeout bounds one background proof. It is the probe's own
// deadline, not the caller's: a hung CPA makes the answer stale, never a request
// slow.
const pluginCapabilityProbeTimeout = 5 * time.Second

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
//   - liveness and readiness endpoints answer for the process, not for a Console
//     page, and a monitor must never be the request that triggers a CPA round
//     trip;
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
	if requestPath == "/health" || requestPath == "/healthz" ||
		requestPath == "/ready" || requestPath == "/readyz" {
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
// resources end to end. It answers with the CPA's own configuration document: a
// v8 build exposes the plugin section there, and a connection that cannot produce
// it cannot serve a plugin page either.
//
// The probe is a network round trip to CPA, so it never runs on a request. A
// read serves the last proven answer and, when that answer is stale, asks for a
// background refresh and returns immediately. Three properties follow, and each
// is what the previous synchronous version got wrong:
//
//   - a hung or slow CPA cannot stall a request. /health and every other routed
//     response return on their own deadline, not the probe's;
//   - a cancelled request cannot poison the answer, because the refresh runs on
//     its own detached context with its own timeout rather than borrowing the
//     caller's;
//   - at most one refresh is in flight, so a burst of requests after expiry
//     produces one CPA read rather than one per request.
//
// Until the first proof lands the answer is false. The navigation narrows rather
// than advertising a capability that has not been demonstrated.
type pluginCapabilityProbe struct {
	mu       sync.Mutex
	ttl      time.Duration
	timeout  time.Duration
	at       time.Time
	proven   bool
	proving  bool
	probe    func(context.Context) bool
	now      func() time.Time
	launch   func(func())
}

func newPluginCapabilityProbe(ttl, timeout time.Duration, probe func(context.Context) bool) *pluginCapabilityProbe {
	return &pluginCapabilityProbe{
		ttl:     ttl,
		timeout: timeout,
		probe:   probe,
		now:     time.Now,
		launch:  func(refresh func()) { go refresh() },
	}
}

// capable reports the last proven answer and refreshes it in the background when
// it is stale. It never blocks on the probe.
func (p *pluginCapabilityProbe) capable(context.Context) bool {
	if p == nil || p.probe == nil {
		return false
	}
	p.mu.Lock()
	proven := p.proven
	stale := p.at.IsZero() || p.now().Sub(p.at) >= p.ttl
	start := stale && !p.proving
	if start {
		p.proving = true
	}
	p.mu.Unlock()
	if start {
		p.launch(p.refresh)
	}
	return proven
}

// refresh performs one proof and stores its answer. It is the single place the
// probe is called, so the detached context and the timeout are impossible to
// forget.
func (p *pluginCapabilityProbe) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	proven := false
	defer func() {
		// A panic in the probe must not leave proving set, which would stop every
		// later refresh.
		if recovered := recover(); recovered != nil {
			proven = false
		}
		p.mu.Lock()
		p.proven = proven
		p.at = p.now()
		p.proving = false
		p.mu.Unlock()
	}()
	proven = p.probe(ctx)
}

// forget drops the stored answer and lets the next read prove again. A profile
// change - a managed install committing, a connection being edited - must not
// inherit the previous connection's answer.
func (p *pluginCapabilityProbe) forget() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.at = time.Time{}
	p.proven = false
	p.mu.Unlock()
}

// v8PluginCapable runs one proof against the saved connection.
func (a *freshApplication) v8PluginCapable(ctx context.Context) bool {
	if a == nil || a.gatewayConfig == nil {
		return false
	}
	return a.gatewayConfig.SupportsPlugins(ctx)
}
