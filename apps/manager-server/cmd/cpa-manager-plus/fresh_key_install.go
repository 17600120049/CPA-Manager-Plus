package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeyapply"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
)

// Canonical Admin authentication has already succeeded. No request may submit
// runtime identity, generation, exclusivity, credential, URL or proof flags.
func (a *freshApplication) initialClientKey(w http.ResponseWriter, r *http.Request, op bootstrap.FreshOperation, id string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(405)
		return
	}
	if _, err := a.keys.Detail(r.Context(), id); err != nil {
		if errors.Is(err, freshkeys.ErrRequest) {
			w.WriteHeader(400)
		} else if errors.Is(err, freshkeys.ErrNotFound) {
			w.WriteHeader(404)
		} else {
			w.WriteHeader(503)
		}
		return
	}
	if r.Method == http.MethodPost {
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(415)
			return
		}
		var body *struct{}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128))
		d.DisallowUnknownFields()
		var trailing any
		if d.Decode(&body) != nil || body == nil || d.Decode(&trailing) != io.EOF {
			w.WriteHeader(400)
			return
		}
	}
	if a.keyApply == nil || !a.keyApply.Targets(op, freshkeyapply.SurfaceKeyInstall) {
		if r.Method == http.MethodPost {
			w.WriteHeader(409)
			return
		}
		writeFreshJSON(w, freshkeyapply.State{KeyID: id, ManagerStaged: true, RuntimeState: "effect_unknown"})
		return
	}
	var result freshkeyapply.State
	var err error
	if r.Method == http.MethodPost {
		result, err = a.keyApply.Apply(r.Context(), id, op.Strategy.TargetGeneration)
	} else {
		result, err = a.keyApply.Observe(r.Context(), id, op.Strategy.TargetGeneration)
	}
	if err != nil {
		code := "s1_observation_unavailable"
		w.Header().Set("Content-Type", "application/json")
		switch {
		case errors.Is(err, freshkeys.ErrConflict):
			code = "s1_attempt_already_consumed"
			w.WriteHeader(409)
		case errors.Is(err, freshkeyapply.ErrGeneration):
			code = "s1_generation_mismatch"
			w.WriteHeader(409)
		case errors.Is(err, cpa.ErrSourceConflict):
			code = "s1_source_conflict"
			w.WriteHeader(409)
		case errors.Is(err, freshkeyapply.ErrUnavailable):
			code = "s1_host_proof_unavailable"
			w.WriteHeader(409)
		default:
			if errors.Is(err, cpa.ErrApplyUnknown) {
				code = "s1_write_unknown"
			}
			w.WriteHeader(503)
		}
		writeFreshJSON(w, struct {
			Code string `json:"code"`
		}{code})
		return
	}
	writeFreshJSON(w, result)
}
