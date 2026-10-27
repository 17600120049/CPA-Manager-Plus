package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshconfig"
)

// exactObject rejects duplicate/case-folded/unknown keys, null, trailing data and
// wrong container types. Only the two bounded request objects use it.
func exactObject(b []byte, keys ...string) bool {
	d := json.NewDecoder(bytes.NewReader(b))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		t, e = d.Token()
		k, ok := t.(string)
		if e != nil || !ok || seen[k] {
			return false
		}
		allowed := false
		for _, name := range keys {
			if name == k {
				allowed = true
			}
		}
		if !allowed {
			return false
		}
		seen[k] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return false
		}
	}
	t, e = d.Token()
	return e == nil && t == json.Delim('}') && len(seen) == len(keys) && d.Decode(new(any)) == io.EOF
}

func configBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Content-Encoding") != "" {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return nil, false
	}
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 2048))
	if e != nil {
		w.WriteHeader(http.StatusBadRequest)
		return nil, false
	}
	return b, true
}

func configReply(w http.ResponseWriter, v any, e error) {
	if o, ok := v.(freshstore.ConfigOperation); ok {
		v = struct {
			Operation freshstore.ConfigOperation `json:"operation"`
			Effective string                     `json:"effective"`
		}{o, "unknown"}
	}
	if e == nil {
		writeFreshJSON(w, v)
		return
	}
	code, status := "configuration_unavailable", http.StatusServiceUnavailable
	switch {
	case errors.Is(e, freshconfig.ErrInvalid):
		code, status = "invalid_config_field", http.StatusBadRequest
	case errors.Is(e, freshconfig.ErrNotFound):
		code, status = "operation_not_found", http.StatusNotFound
	case errors.Is(e, cpa.ErrSourceConflict), errors.Is(e, freshstore.ErrConflict):
		code, status = "source_or_operation_conflict", http.StatusConflict
	case errors.Is(e, cpa.ErrProofUnavailable):
		code, status = "private_source_not_authorized", http.StatusConflict
	case errors.Is(e, cpa.ErrWriterIsolation):
		code, status = "config_key_writer_not_isolated", http.StatusConflict
	case errors.Is(e, cpa.ErrApplyUnknown):
		code, status = "original_operation_unknown", http.StatusConflict
	case errors.Is(e, freshconfig.ErrWriteFailed):
		// A definite terminal failure: the original never landed and the field is
		// writable again, so this is not an unknown-result conflict.
		code, status = "config_field_write_failed", http.StatusUnprocessableEntity
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeFreshJSON(w, struct {
		Code   string `json:"code"`
		Result any    `json:"result,omitempty"`
	}{code, v})
}

// configFieldConfiguration serves the generic allowlisted field surface:
//
//	GET  gateway/config/requests/streaming/keepalive-seconds
//	PUT  gateway/config/requests/streaming/keepalive-seconds
//	GET  gateway/config/operations/{requestId}
//	POST gateway/config/operations/{requestId}/reconcile
//
// The canonical Public API has already verified the persisted Administrator
// credential. No supplied URL, key, proof flag, YAML or recovery bytes exist;
// a path outside the allowlist is a 404 and never reaches CPA.
func (a *freshApplication) configFieldConfiguration(w http.ResponseWriter, r *http.Request, resource string) {
	if r.URL.RawQuery != "" || r.URL.EscapedPath() != r.URL.Path {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	parts := strings.Split(resource, "/")
	if parts[0] == "operations" {
		a.configFieldOperation(w, r, parts[1:])
		return
	}
	path := strings.Join(parts, ".")
	if _, ok := cpa.LookupConfigField(path); !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodGet {
		if a.configRetry == nil {
			configReply(w, nil, cpa.ErrProofUnavailable)
			return
		}
		v, e := a.configRetry.ReadField(r.Context(), path)
		configReply(w, v, e)
		return
	}
	// Input validation never depends on whether this profile has a writer.
	b, ok := configBody(w, r)
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	if !exactObject(b, "requestId", "value", "expected") || json.Unmarshal(b, &raw) != nil ||
		!exactObject(raw["expected"], "sourceId", "generation", "revision", "sha256") {
		configReply(w, nil, freshconfig.ErrInvalid)
		return
	}
	var q freshconfig.FieldRequest
	if json.Unmarshal(b, &q) != nil {
		configReply(w, nil, freshconfig.ErrInvalid)
		return
	}
	if e := freshconfig.ValidateFieldRequest(path, q); e != nil {
		configReply(w, nil, e)
		return
	}
	if a.configRetry == nil {
		configReply(w, nil, cpa.ErrProofUnavailable)
		return
	}
	v, e := a.configRetry.SaveField(r.Context(), path, q)
	configReply(w, v, e)
}

// configFieldOperation inspects the ORIGINAL operation only. Reconcile asks the
// Host to verify the admitted file; it never repeats the PUT.
func (a *freshApplication) configFieldOperation(w http.ResponseWriter, r *http.Request, parts []string) {
	reconcile := len(parts) == 2 && parts[1] == "reconcile"
	if len(parts) != 1 && !reconcile {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	method := http.MethodGet
	if reconcile {
		method = http.MethodPost
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if reconcile {
		b, ok := configBody(w, r)
		if !ok {
			return
		}
		if !exactObject(b) {
			configReply(w, nil, freshconfig.ErrInvalid)
			return
		}
	}
	if a.configRetry == nil {
		configReply(w, nil, cpa.ErrProofUnavailable)
		return
	}
	v, e := a.configRetry.Operation(r.Context(), id, reconcile)
	configReply(w, v, e)
}
