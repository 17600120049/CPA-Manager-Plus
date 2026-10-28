package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshconfig"
)

// pluginRuntime serves the plugin runtime switch: the plugin-scoped sibling of
// the gateway configuration page.
//
//	GET  plugins/runtime
//	POST plugins/runtime/groups/plugins
//	GET  plugins/runtime/operations/{requestId}
//	POST plugins/runtime/operations/{requestId}/reconcile
//
// It shares the page's journal, CAS and read-back machinery, so a switch write is
// one journaled operation that a lost reply settles by reading back. Only the
// managed default-install authority resolves for this scope; every other profile
// is answered with the closed refusal instead of a value it does not own.
func (a *freshApplication) pluginRuntime(w http.ResponseWriter, r *http.Request, resource string) {
	if r.URL.RawQuery != "" || r.URL.EscapedPath() != r.URL.Path {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Request shape is validated before any profile question: an invalid body is
	// a 400 whether or not this installation holds a plugin authority.
	parts := strings.Split(resource, "/")
	switch {
	case resource == "":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		service := a.gatewayService(w)
		if service == nil {
			return
		}
		view, err := service.ReadPlugins(r.Context())
		gatewayConfigReply(w, view, err)
	case parts[0] == "groups" && len(parts) == 2:
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		body, ok := gatewayConfigBody(w, r)
		if !ok {
			return
		}
		if !exactObject(body, "requestId", "expected", "edits") || !exactExpected(body) {
			gatewayConfigReply(w, nil, freshconfig.ErrInvalid)
			return
		}
		var q freshconfig.GroupRequest
		if json.Unmarshal(body, &q) != nil {
			gatewayConfigReply(w, nil, freshconfig.ErrInvalid)
			return
		}
		if err := freshconfig.ValidatePluginGroupRequest(parts[1], q); err != nil {
			gatewayConfigReply(w, nil, err)
			return
		}
		service := a.gatewayService(w)
		if service == nil {
			return
		}
		operation, err := service.SavePluginGroup(r.Context(), parts[1], q)
		gatewayConfigReply(w, operation, err)
	case parts[0] == "operations" && (len(parts) == 2 || len(parts) == 3 && parts[2] == "reconcile"):
		a.pluginRuntimeOperation(w, r, parts)
	default:
		http.NotFound(w, r)
	}
}

// pluginRuntimeOperation inspects the ORIGINAL plugin switch operation only.
// Reconcile asks CPA for its document and never repeats the write.
func (a *freshApplication) pluginRuntimeOperation(w http.ResponseWriter, r *http.Request, parts []string) {
	reconcile := len(parts) == 3
	method := http.MethodGet
	if reconcile {
		method = http.MethodPost
	}
	if r.Method != method {
		methodNotAllowed(w, method)
		return
	}
	if reconcile {
		b, ok := gatewayConfigBody(w, r)
		if !ok {
			return
		}
		if !exactObject(b) {
			gatewayConfigReply(w, nil, freshconfig.ErrInvalid)
			return
		}
	}
	service := a.gatewayService(w)
	if service == nil {
		return
	}
	operation, err := service.PluginOperation(r.Context(), parts[1], reconcile)
	gatewayConfigReply(w, operation, err)
}
