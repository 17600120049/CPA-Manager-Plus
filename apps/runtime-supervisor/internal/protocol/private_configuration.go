package protocol

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/privateconfig"
)

type PrivateConfigurationExecutor interface {
	PrivateConfiguration(context.Context, privateconfig.Request) (privateconfig.Result, error)
}

type privateConfigurationHandler struct {
	*handler
	privateConfiguration PrivateConfigurationExecutor
}

// WithPrivateConfiguration is used only by the separate opt-in Host profile.
// The ordinary protocol surface and its qualification bytes stay unchanged.
func WithPrivateConfiguration(base http.Handler, executor PrivateConfigurationExecutor) http.Handler {
	h := base.(*handler)
	if executor == nil {
		return base
	}
	return &privateConfigurationHandler{handler: h, privateConfiguration: executor}
}
func (h *privateConfigurationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != privateconfig.Path {
		h.handler.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if !h.authorized(r.Header.Get("Authorization")) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, 401, "unauthorized", "request is not authorized")
		return
	}
	if r.URL.RawQuery != "" || r.URL.EscapedPath() != r.URL.Path {
		writeError(w, 400, "invalid_request", "invalid private configuration request")
		return
	}
	var q privateconfig.Request
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&q) != nil || d.Decode(new(any)) != io.EOF {
		writeError(w, 400, "invalid_request", "invalid private configuration request")
		return
	}
	v, e := h.privateConfiguration.PrivateConfiguration(r.Context(), q)
	if e != nil {
		writeError(w, 409, "private_configuration_stop", "private configuration requires Host reconciliation")
		return
	}
	writeJSON(w, 200, v)
}
