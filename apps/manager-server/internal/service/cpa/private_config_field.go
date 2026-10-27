package cpa

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
)

// ErrWriterIsolation reports a config source whose key writer is not isolated.
var ErrWriterIsolation = errors.New("private config source has a reachable key writer")

// ConfigFieldSource is the Runtime observation of one admitted configuration
// source. It contains Runtime metadata only: never YAML, a management key or a
// raw config. The gateway page is group-based and no longer creates these
// observations; the type survives so an operation journaled before the cutover
// still reconciles against the Runtime record that admitted it.
type ConfigFieldSource struct {
	H1             bool   `json:"h1,omitempty"`
	ParentID       string `json:"parentId,omitempty"`
	HandoffValid   bool   `json:"handoffValid,omitempty"`
	SourceID       string `json:"sourceId"`
	Generation     string `json:"generation"`
	Revision       uint64 `json:"revision"`
	SHA256         string `json:"sha256"`
	State          string `json:"state"`
	Installed      bool   `json:"installed"`
	OperationID    string `json:"operationId"`
	OperationState string `json:"operationState"`
	OperationScope string `json:"operationScope"`
	OperationPath  string `json:"operationPath"`
	OperationValue any    `json:"operationValue,omitempty"`
}

// legacyOperationPath maps a pre-table Runtime operation scope onto its field.
func legacyOperationPath(scope string) (string, bool) {
	switch scope {
	case "request-retry":
		return "routing.retry.request-retry", true
	case "max-retry-interval":
		return "routing.retry.max-retry-interval", true
	default:
		return "", false
	}
}

// operationField resolves the field an observation's original operation wrote,
// including operations reserved before the table replaced the short allowlist.
func operationField(op configOperation) (string, any, bool) {
	if op.Path != "" {
		value := NormalizeConfigValue(op.Value)
		if ValidateConfigLeafValue(op.Path, value) != nil {
			return "", nil, false
		}
		return op.Path, value, true
	}
	path, ok := legacyOperationPath(op.Scope)
	if !ok {
		return "", nil, false
	}
	switch path {
	case "routing.retry.request-retry":
		return path, op.Retry, true
	default:
		return path, op.MaxRetryInterval, true
	}
}

// ConfigFieldRequestID is the journal request ID behind a Runtime operation ID.
// Pre-table direct writes prefixed it; an H1 step embeds its parent request ID
// ("c:<requestId>:<step>:<scope>") and is therefore resolved back to it.
func ConfigFieldRequestID(id string) string {
	id = strings.TrimPrefix(id, "retry-")
	parent, ok := strings.CutPrefix(id, "c:")
	if !ok {
		return id
	}
	// The step scope may contain dots but never a colon, so the third separator
	// ends the request ID.
	parts := strings.SplitN(parent, ":", 3)
	if len(parts) != 3 || parts[0] == "" {
		return id
	}
	return parts[0]
}

func operationRequestID(id string) string { return ConfigFieldRequestID(id) }

func configFieldSource(v configObservation) ConfigFieldSource {
	out := ConfigFieldSource{
		SourceID:       v.SourceID,
		Generation:     v.Generation,
		Revision:       v.Revision,
		SHA256:         v.SHA256,
		State:          v.State,
		Installed:      v.Installed,
		OperationID:    v.Operation.ID,
		OperationState: v.Operation.State,
		OperationScope: v.Operation.Scope,
	}
	if path, value, ok := operationField(v.Operation); ok {
		out.OperationPath, out.OperationValue = path, value
	}
	if v.H1 != nil {
		out.H1 = true
		if v.H1.Parent != nil {
			out.ParentID = v.H1.Parent.ID
		}
	}
	return out
}

func sameConfigFieldSource(a, b ConfigFieldSource) bool {
	return a.H1 == b.H1 && a.ParentID == b.ParentID && a.HandoffValid == b.HandoffValid &&
		a.SourceID == b.SourceID && a.Generation == b.Generation && a.Revision == b.Revision &&
		a.SHA256 == b.SHA256 && a.State == b.State && a.Installed == b.Installed &&
		a.OperationID == b.OperationID && a.OperationState == b.OperationState && a.OperationScope == b.OperationScope &&
		a.OperationPath == b.OperationPath && reflect.DeepEqual(a.OperationValue, b.OperationValue)
}

// ReadConfigFieldValue reads one leaf with the caller's management authority.
// It is the read-back half of legacy reconciliation: the page never writes a
// single leaf any more, but an operation journaled before the cutover is still
// resolved by what CPA now holds. A path outside the table is refused.
func (t *Transport) ReadConfigFieldValue(ctx context.Context, management, path string) (string, any, error) {
	endpoint, err := ConfigLeafEndpoint(path)
	if err != nil {
		return "unknown", nil, ErrProofUnavailable
	}
	code, body, err := t.request(ctx, http.MethodGet, endpoint, management, nil)
	if err != nil {
		return "unknown", nil, err
	}
	switch code {
	case http.StatusNotFound:
		return "missing", nil, nil
	case http.StatusOK:
		trimmed := bytes.TrimSpace(body)
		if bytes.Equal(trimmed, []byte("null")) {
			return "null", nil, nil
		}
		value, err := ParseConfigLeafValue(path, trimmed)
		if err != nil {
			return "unknown", nil, ErrSourceConflict
		}
		return "present", value, nil
	default:
		return "unknown", nil, ErrProofUnavailable
	}
}

// HasRuntimeJournal reports whether this Transport reserves through the private
// runtime authority. Without it the Manager journal is the only reservation.
func (t *Transport) HasRuntimeJournal() bool {
	return t.configurationAuthority != nil || t.h1 != nil
}

// ConfigFieldSource reports the private source and the single H1 handoff state.
func (t *Transport) ConfigFieldSource(ctx context.Context) (ConfigFieldSource, error) {
	if t.h1 == nil {
		_, v, err := t.configuration(ctx, "observe")
		return configFieldSource(v), err
	}
	v, err := t.h1.Ready(ctx, t)
	out := configFieldSource(v)
	if err != nil {
		return out, err
	}
	if v.H1.Parent == nil {
		out.HandoffValid = t.h1.Free(ctx, t) == nil
	}
	return out, nil
}

// ReconcileConfigField records only the ORIGINAL Runtime operation after the
// Host verified the complete admitted file. It never repeats the PUT.
func (t *Transport) ReconcileConfigField(ctx context.Context, source ConfigFieldSource, requestID, path string, value any) (ConfigFieldSource, error) {
	v, err := t.ConfigFieldSource(ctx)
	if err != nil || v.SourceID != source.SourceID || v.Generation != source.Generation ||
		v.OperationPath != path || operationRequestID(v.OperationID) != requestID {
		return v, ErrSourceConflict
	}
	if v.State == "persisted" && (v.OperationState == "persisted" || v.OperationState == "recovered") {
		return v, nil
	}
	if v.OperationState != "unknown" || t.configurationAuthority == nil {
		return v, ErrApplyUnknown
	}
	result, err := t.configurationAuthority.call(ctx, configRequest{
		Action:           "commit",
		SourceID:         v.SourceID,
		Generation:       v.Generation,
		ExpectedRevision: v.Revision,
		ExpectedSHA256:   v.SHA256,
		OperationID:      v.OperationID,
		Scope:            path,
		Path:             path,
		Value:            value,
	})
	if err != nil || result.State != "persisted" || result.Operation.ID != v.OperationID ||
		result.Operation.State != "persisted" {
		return v, ErrApplyUnknown
	}
	// The Runtime reports the original operation, which may carry a pre-table
	// scope: bind it to the field it wrote, never to the raw scope string.
	observedPath, observedValue, ok := operationField(result.Operation)
	if !ok || observedPath != path || !reflect.DeepEqual(observedValue, value) {
		return v, ErrApplyUnknown
	}
	return configFieldSource(result), nil
}
