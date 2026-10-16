package main

import (
	"net/http"
	"sync/atomic"

	bootstrap "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	routingdomain "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/routing"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/bootstrapdiscovery"
)

// startupHTTPHandler owns the fixed discovery path while the normal Manager
// graph is still being constructed. Before ready, every unrelated path is
// rejected locally; after ready, only the existing application handler is
// delegated to. The fixed path remains captured for the lifetime of the
// process, including the future completed terminal behavior.
type startupHTTPHandler struct {
	discovery routingdomain.DiscoveryHandler
	liveness  http.Handler
	protected http.Handler
	next      atomic.Value // *startupHandlerTarget
	lifecycle atomic.Value // routingdomain.DiscoveryLifecycle
	ready     atomic.Bool
}

type startupHandlerTarget struct {
	handler http.Handler
}

func newStartupHTTPHandler(result bootstrap.PublicDiscovery) *startupHTTPHandler {
	h := &startupHTTPHandler{liveness: bootstrapdiscovery.LivenessHandler{}}
	h.lifecycle.Store(routingdomain.DiscoveryActive)
	h.discovery = routingdomain.DiscoveryHandler{
		Lifecycle: h.discoveryLifecycle,
		Active:    bootstrapdiscovery.Handler{Discovery: result},
	}
	h.protected = routingdomain.ProtectFixedDiscovery(http.HandlerFunc(h.serveNonDiscovery), h.discovery)
	h.next.Store(&startupHandlerTarget{handler: http.NotFoundHandler()})
	return h
}

func (h *startupHTTPHandler) discoveryLifecycle() routingdomain.DiscoveryLifecycle {
	lifecycle, _ := h.lifecycle.Load().(routingdomain.DiscoveryLifecycle)
	if lifecycle == "" {
		return routingdomain.DiscoveryUnavailable
	}
	return lifecycle
}

func (h *startupHTTPHandler) SetReady(next http.Handler) {
	if next == nil {
		next = http.NotFoundHandler()
	}
	h.next.Store(&startupHandlerTarget{handler: next})
	h.ready.Store(true)
}

func (h *startupHTTPHandler) SetCompleted() {
	h.lifecycle.Store(routingdomain.DiscoveryCompleted)
}

func (h *startupHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.protected.ServeHTTP(w, r)
}

func (h *startupHTTPHandler) serveNonDiscovery(w http.ResponseWriter, r *http.Request) {
	if !h.ready.Load() && r != nil && r.URL != nil && r.URL.Path == bootstrapdiscovery.HealthPath {
		h.liveness.ServeHTTP(w, r)
		return
	}
	target, _ := h.next.Load().(*startupHandlerTarget)
	if target == nil || target.handler == nil {
		http.NotFound(w, r)
		return
	}
	target.handler.ServeHTTP(w, r)
}
