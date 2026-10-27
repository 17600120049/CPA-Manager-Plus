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

// ConfigFieldSource is the Runtime observation for one bounded field write. It
// contains Runtime metadata only: never YAML, a management key or a raw config.
// SHA256 is always the admitted raw-file digest, never ConfigV8's normalized GET.
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

// legacyOperationPath maps a pre-allowlist Runtime operation scope onto its field.
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
// including operations reserved before the allowlist cutover.
func operationField(op configOperation) (string, any, bool) {
	if op.Path != "" {
		value := NormalizeConfigValue(op.Value)
		if ValidateConfigFieldValue(op.Path, value) != nil {
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
// Pre-allowlist direct writes prefixed it; H1 step IDs are resolved from the H1
// parent instead and therefore never match a request ID.
func ConfigFieldRequestID(id string) string {
	return strings.TrimPrefix(id, "retry-")
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

// ReadConfigFieldValue reads one allowlisted field with the caller's management
// authority. It is the read half of the managed profile, whose journal is owned
// by the Manager; it never reaches CPA for a path outside the allowlist.
func (t *Transport) ReadConfigFieldValue(ctx context.Context, management, path string) (string, any, error) {
	endpoint, err := ConfigFieldEndpoint(path)
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
		value, err := ParseConfigFieldValue(path, trimmed)
		if err != nil {
			return "unknown", nil, ErrSourceConflict
		}
		return "present", value, nil
	default:
		return "unknown", nil, ErrProofUnavailable
	}
}

// WriteConfigFieldValue performs exactly one non-replayable PUT of one scalar.
func (t *Transport) WriteConfigFieldValue(ctx context.Context, management, path string, value any) error {
	endpoint, err := ConfigFieldEndpoint(path)
	if err != nil {
		return ErrProofUnavailable
	}
	payload, err := EncodeConfigFieldValue(path, value)
	if err != nil {
		return ErrProofUnavailable
	}
	code, _, err := t.request(ctx, http.MethodPut, endpoint, management, payload)
	if err != nil || (code != http.StatusOK && code != http.StatusNoContent) {
		return ErrApplyUnknown
	}
	return nil
}

// HasRuntimeJournal reports whether this Transport reserves through the private
// runtime authority. Without it the Manager journal is the only reservation.
func (t *Transport) HasRuntimeJournal() bool {
	return t.configurationAuthority != nil || t.h1 != nil
}

// ConfigFieldSource reports the private source and the single H1 handoff state.
// A private H1 profile must additionally prove its handoff is ready and free.
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

// ReadConfigField retains missing, explicit null and present independently. The
// observation must be stable across the scoped CPA read; this is not an atomic
// upstream CAS and makes no effectiveness claim.
func (t *Transport) ReadConfigField(ctx context.Context, management, path string) (ConfigFieldSource, string, any, error) {
	if _, ok := LookupConfigField(path); !ok {
		return ConfigFieldSource{}, "unknown", nil, ErrProofUnavailable
	}
	before, err := t.ConfigFieldSource(ctx)
	if err != nil {
		return before, "unknown", nil, err
	}
	endpoint, err := ConfigFieldEndpoint(path)
	if err != nil {
		return before, "unknown", nil, err
	}
	code, body, err := t.request(ctx, http.MethodGet, endpoint, management, nil)
	if err != nil {
		return before, "unknown", nil, err
	}
	presence := "missing"
	var value any
	switch code {
	case http.StatusNotFound:
	case http.StatusOK:
		trimmed := bytes.TrimSpace(body)
		if bytes.Equal(trimmed, []byte("null")) {
			presence = "null"
			break
		}
		value, err = ParseConfigFieldValue(path, trimmed)
		if err != nil {
			return before, "unknown", nil, ErrSourceConflict
		}
		presence = "present"
	default:
		return before, "unknown", nil, ErrProofUnavailable
	}
	after, err := t.ConfigFieldSource(ctx)
	if err != nil || !sameConfigFieldSource(before, after) {
		return after, "unknown", nil, ErrSourceConflict
	}
	return after, presence, value, nil
}

// BeginConfigField durably reserves the original operation. It never retries a
// reservation: the caller has already recorded this request ID in the journal.
func (t *Transport) BeginConfigField(ctx context.Context, source ConfigFieldSource, id, path string, value any) error {
	if ValidateConfigFieldValue(path, value) != nil || t.configurationAuthority == nil {
		return ErrProofUnavailable
	}
	grant, err := t.configurationAuthority.grant()
	if err != nil || grant.SourceID != source.SourceID || grant.Generation != source.Generation {
		return ErrSourceConflict
	}
	v, err := t.configurationAuthority.call(ctx, configRequest{
		Action:           "begin",
		SourceID:         source.SourceID,
		Generation:       source.Generation,
		ExpectedRevision: source.Revision,
		ExpectedSHA256:   source.SHA256,
		OperationID:      id,
		Scope:            path,
		Path:             path,
		Value:            value,
	})
	if err != nil || !v.Created || v.State != "unknown" || v.Operation.ID != id || v.Operation.Scope != path {
		return ErrApplyUnknown
	}
	if observedPath, observedValue, ok := operationField(v.Operation); !ok || observedPath != path || !reflect.DeepEqual(observedValue, value) {
		return ErrApplyUnknown
	}
	return nil
}

// PutConfigField sends exactly one non-replayable PUT of one scalar. No
// redirects, retries, YAML, generic path or fallback; a transport error is
// always an unknown result.
func (t *Transport) PutConfigField(ctx context.Context, source ConfigFieldSource, id, management, path string, value any) error {
	if ValidateConfigFieldValue(path, value) != nil {
		return ErrProofUnavailable
	}
	v, err := t.ConfigFieldSource(ctx)
	if err != nil || v.SourceID != source.SourceID || v.Generation != source.Generation ||
		v.Revision != source.Revision+1 || v.SHA256 != source.SHA256 || v.OperationState != "unknown" ||
		v.OperationID != id || v.OperationPath != path || !reflect.DeepEqual(v.OperationValue, value) {
		return ErrApplyUnknown
	}
	endpoint, err := ConfigFieldEndpoint(path)
	if err != nil {
		return ErrProofUnavailable
	}
	payload, err := EncodeConfigFieldValue(path, value)
	if err != nil {
		return ErrProofUnavailable
	}
	code, _, err := t.request(ctx, http.MethodPut, endpoint, management, payload)
	if err != nil || (code != http.StatusOK && code != http.StatusNoContent) {
		return ErrApplyUnknown
	}
	return nil
}

// WriteH1ConfigField writes the one step already reserved by the H1 parent. The
// intent is re-read from Runtime, never trusted from local state.
func (t *Transport) WriteH1ConfigField(ctx context.Context, parent string, step H1Step, management, path string, value any) error {
	if t.h1 == nil || ValidateConfigFieldValue(path, value) != nil || step.Scope != path {
		return ErrProofUnavailable
	}
	v, err := t.h1.Parent(ctx, t, parent)
	if err != nil {
		return err
	}
	if v.H1 == nil || v.H1.Parent == nil {
		return ErrSourceConflict
	}
	intentPath, intentValue, ok := v.H1.Parent.Intent.fieldIntent()
	if !ok || intentPath != path || !reflect.DeepEqual(intentValue, value) {
		return ErrSourceConflict
	}
	if err = t.h1.Reserved(ctx, t, parent, step); err != nil {
		return err
	}
	endpoint, err := ConfigFieldEndpoint(path)
	if err != nil {
		return ErrProofUnavailable
	}
	payload, err := EncodeConfigFieldValue(path, value)
	if err != nil {
		return ErrProofUnavailable
	}
	code, _, err := t.request(ctx, http.MethodPut, endpoint, management, payload)
	if err != nil || (code != http.StatusOK && code != http.StatusNoContent) {
		return ErrApplyUnknown
	}
	return nil
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
	// The Runtime reports the original operation, which may carry a pre-allowlist
	// scope: bind it to the field it wrote, never to the raw scope string.
	observedPath, observedValue, ok := operationField(result.Operation)
	if !ok || observedPath != path || !reflect.DeepEqual(observedValue, value) {
		return v, ErrApplyUnknown
	}
	return configFieldSource(result), nil
}
