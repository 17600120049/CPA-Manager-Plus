package main

import (
	"net/http"
	"sync/atomic"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/bootstrapdiscovery"
)

// startupHTTPHandler owns the fixed discovery path while the normal Manager
// graph is still being constructed. Before ready, every unrelated path is
// rejected locally; after ready, only the existing application handler is
// delegated to. The fixed path remains captured for the lifetime of the
// process, including the future completed terminal behavior.
type startupHTTPHandler struct {
	discovery bootstrapdiscovery.Handler
	next      atomic.Value // *startupHandlerTarget
	ready     atomic.Bool
}

type startupHandlerTarget struct {
	handler http.Handler
}

func newStartupHTTPHandler(result bootstrapdiscovery.Result) *startupHTTPHandler {
	h := &startupHTTPHandler{discovery: bootstrapdiscovery.Handler{Result: result}}
	h.next.Store(&startupHandlerTarget{handler: http.NotFoundHandler()})
	return h
}

func (h *startupHTTPHandler) SetReady(next http.Handler) {
	if next == nil {
		next = http.NotFoundHandler()
	}
	h.next.Store(&startupHandlerTarget{handler: next})
	h.ready.Store(true)
}

func (h *startupHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == bootstrapdiscovery.Path || (!h.ready.Load() && r.URL.Path == bootstrapdiscovery.HealthPath) {
		h.discovery.ServeHTTP(w, r)
		return
	}
	target, _ := h.next.Load().(*startupHandlerTarget)
	if target == nil || target.handler == nil {
		http.NotFound(w, r)
		return
	}
	target.handler.ServeHTTP(w, r)
}
