package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
)

// Called only after canonical routing and the existing Admin verifier succeed.
func (a *freshApplication) clientKeys(w http.ResponseWriter, r *http.Request, op bootstrap.FreshOperation, resource string) {
	if !op.GatewayPending || op.Strategy.Strategy != "" && op.Strategy.Strategy != bootstrap.NewManaged {
		w.WriteHeader(http.StatusConflict)
		return
	}
	switch {
	case resource == "api-keys" && r.Method == http.MethodGet:
		keys, err := a.keys.List(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeFreshJSON(w, struct {
			Keys                []freshkeys.Key `json:"keys"`
			GatewayPending      bool            `json:"gatewayPending"`
			RevocationAvailable bool            `json:"revocationAvailable"`
		}{keys, true, false})
	case strings.HasPrefix(resource, "api-keys/requests/") && r.Method == http.MethodGet:
		id := strings.TrimPrefix(resource, "api-keys/requests/")
		key, err := a.keys.Lookup(r.Context(), id)
		if errors.Is(err, freshkeys.ErrRequest) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if errors.Is(err, freshkeys.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeFreshJSON(w, key)
	case resource == "api-keys" && r.Method == http.MethodPost:
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		var request struct {
			RequestID string `json:"requestId"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		d.DisallowUnknownFields()
		if d.Decode(&request) != nil || !freshkeys.ValidRequestID(request.RequestID) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var trailing any
		if d.Decode(&trailing) != io.EOF {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		result, created, err := a.keys.Create(r.Context(), request.RequestID)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if created {
			w.WriteHeader(http.StatusCreated)
		}
		writeFreshJSON(w, result)
	default:
		// Including the final staged key: no delete/replace/reveal route exists.
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
