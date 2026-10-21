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

// exactObject rejects duplicate/case-folded/unknown keys, null, trailing data
// and wrong container types. Only the two bounded request objects use it.
func retryObject(b []byte, keys ...string) bool {
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
func retryBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Content-Encoding") != "" {
		w.WriteHeader(415)
		return nil, false
	}
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 2048))
	if e != nil {
		w.WriteHeader(400)
		return nil, false
	}
	return b, true
}
func retryReply(w http.ResponseWriter, v any, e error) {
	if o, ok := v.(freshstore.RetryOperation); ok {
		v = struct {
			Operation freshstore.RetryOperation `json:"operation"`
			Effective string                    `json:"effective"`
		}{o, "unknown"}
	}
	if e == nil {
		writeFreshJSON(w, v)
		return
	}
	code, status := "configuration_unavailable", 503
	switch {
	case errors.Is(e, freshconfig.ErrInvalid):
		code, status = "invalid_retry_request", 400
	case errors.Is(e, freshconfig.ErrNotFound):
		code, status = "operation_not_found", 404
	case errors.Is(e, cpa.ErrSourceConflict), errors.Is(e, freshstore.ErrConflict):
		code, status = "source_or_operation_conflict", 409
	case errors.Is(e, cpa.ErrProofUnavailable):
		code, status = "private_source_not_authorized", 409
	case errors.Is(e, cpa.ErrWriterIsolation):
		code, status = "config_key_writer_not_isolated", 409
	case errors.Is(e, cpa.ErrApplyUnknown):
		code, status = "original_operation_unknown", 409
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeFreshJSON(w, struct {
		Code   string `json:"code"`
		Result any    `json:"result,omitempty"`
	}{code, v})
}

// The canonical Public API has already verified the persisted Administrator
// credential. No supplied URL, key, proof flag, YAML or recovery bytes exist.
func (a *freshApplication) retryConfiguration(w http.ResponseWriter, r *http.Request, resource string) {
	if r.URL.RawQuery != "" || r.URL.EscapedPath() != r.URL.Path {
		w.WriteHeader(400)
		return
	}
	if resource == "gateway/retry/request-retry" {
		if r.Method != "GET" && r.Method != "PUT" {
			w.Header().Set("Allow", "GET, PUT")
			w.WriteHeader(405)
			return
		}
		var q freshconfig.Request
		if r.Method == "PUT" {
			b, ok := retryBody(w, r)
			if !ok {
				return
			}
			var raw map[string]json.RawMessage
			if !retryObject(b, "requestId", "value", "expected") || json.Unmarshal(b, &raw) != nil || !retryObject(raw["expected"], "sourceId", "generation", "revision", "sha256") || json.Unmarshal(b, &q) != nil || q.Value == nil || *q.Value < 0 || *q.Value > 10 {
				retryReply(w, nil, freshconfig.ErrInvalid)
				return
			}
		}
		if a.configRetry == nil {
			retryReply(w, nil, cpa.ErrProofUnavailable)
			return
		}
		if r.Method == "GET" {
			v, e := a.configRetry.Read(r.Context())
			retryReply(w, v, e)
		} else {
			v, e := a.configRetry.Save(r.Context(), q)
			retryReply(w, v, e)
		}
		return
	}
	id := strings.TrimPrefix(resource, "gateway/retry/operations/")
	reconcile := strings.HasSuffix(id, "/reconcile")
	if reconcile {
		id = strings.TrimSuffix(id, "/reconcile")
	}
	if strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	method := "GET"
	if reconcile {
		method = "POST"
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		w.WriteHeader(405)
		return
	}
	if reconcile {
		b, ok := retryBody(w, r)
		if !ok {
			return
		}
		if !retryObject(b) {
			retryReply(w, nil, freshconfig.ErrInvalid)
			return
		}
	}
	if a.configRetry == nil {
		retryReply(w, nil, cpa.ErrProofUnavailable)
		return
	}
	v, e := a.configRetry.Operation(r.Context(), id, reconcile)
	retryReply(w, v, e)
}
