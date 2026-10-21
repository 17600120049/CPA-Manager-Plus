package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

const retryPath = "/v8/management/config/routing/retry/request-retry"

var ErrWriterIsolation = errors.New("private config source has a reachable key writer")

// RetrySource contains Runtime metadata, never YAML or a secret. SHA256 is
// ALWAYS the admitted raw file digest, never ConfigV8's serialized YAML GET.
type RetrySource struct {
	SourceID       string `json:"sourceId"`
	Generation     string `json:"generation"`
	Revision       uint64 `json:"revision"`
	SHA256         string `json:"sha256"`
	State          string `json:"state"`
	Installed      bool   `json:"installed"`
	OperationID    string `json:"operationId"`
	OperationState string `json:"operationState"`
	OperationScope string `json:"operationScope"`
	OperationRetry int    `json:"operationRetry"`
}

func retrySource(v configObservation) RetrySource {
	return RetrySource{v.SourceID, v.Generation, v.Revision, v.SHA256, v.State, v.Installed, v.Operation.ID, v.Operation.State, v.Operation.Scope, v.Operation.Retry}
}
func (t *Transport) RetrySource(ctx context.Context) (RetrySource, error) {
	_, v, e := t.configuration(ctx, "observe")
	return retrySource(v), e
}

// ReadRetry retains missing/null/zero. An observation must remain stable across
// the scoped read. It is not an atomic upstream CAS or an effectiveness claim.
func (t *Transport) ReadRetry(ctx context.Context, management string) (RetrySource, string, *int, error) {
	a, e := t.RetrySource(ctx)
	if e != nil {
		return a, "unknown", nil, e
	}
	code, b, e := t.request(ctx, http.MethodGet, retryPath, management, nil)
	if e != nil {
		return a, "unknown", nil, e
	}
	presence := "missing"
	var value *int
	switch code {
	case 404:
	case 200:
		presence = "null"
		if !bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
			var n int
			if json.Unmarshal(b, &n) != nil {
				return a, "unknown", nil, ErrSourceConflict
			}
			presence, value = "present", &n
		}
	default:
		return a, "unknown", nil, ErrProofUnavailable
	}
	z, e := t.RetrySource(ctx)
	if e != nil || a != z {
		return z, "unknown", nil, ErrSourceConflict
	}
	return z, presence, value, nil
}

// BeginRetry never retries a reservation. The caller has already durably
// recorded this operation, including the original expected source metadata.
func (t *Transport) BeginRetry(ctx context.Context, source RetrySource, id string, value int) error {
	if value < 0 || value > 10 || t.configurationAuthority == nil {
		return ErrProofUnavailable
	}
	g, e := t.configurationAuthority.grant()
	if e != nil || g.SourceID != source.SourceID || g.Generation != source.Generation {
		return ErrSourceConflict
	}
	v, e := t.configurationAuthority.call(ctx, configRequest{Action: "begin", SourceID: source.SourceID, Generation: source.Generation,
		ExpectedRevision: source.Revision, ExpectedSHA256: source.SHA256, OperationID: id, Scope: "request-retry", Retry: value})
	if e != nil || !v.Created || v.State != "unknown" || v.Operation.ID != id || v.Operation.Scope != "request-retry" || v.Operation.Retry != value {
		return ErrApplyUnknown
	}
	return nil
}

// PutRetry sends exactly one non-replayable PUT. No redirects, retries, YAML,
// generic path or v0 fallback. A transport error is always an unknown result.
func (t *Transport) PutRetry(ctx context.Context, source RetrySource, id string, management string, value int) error {
	if value < 0 || value > 10 {
		return ErrProofUnavailable
	}
	v, e := t.RetrySource(ctx)
	if e != nil || v.SourceID != source.SourceID || v.Generation != source.Generation || v.Revision != source.Revision+1 || v.SHA256 != source.SHA256 || v.OperationID != id || v.OperationScope != "request-retry" || v.OperationRetry != value || v.OperationState != "unknown" {
		return ErrApplyUnknown
	}
	code, _, e := t.request(ctx, http.MethodPut, retryPath, management, []byte(strconv.Itoa(value)))
	if e != nil || code != 200 {
		return ErrApplyUnknown
	}
	return nil
}

// ReconcileRetry records only the original Runtime operation after full-file
// verification/fsync. Controlled recovery remains Host-only with CPA stopped.
func (t *Transport) ReconcileRetry(ctx context.Context, source RetrySource, id string, value int) (RetrySource, error) {
	v, e := t.RetrySource(ctx)
	if e != nil || v.SourceID != source.SourceID || v.Generation != source.Generation || v.OperationID != id || v.OperationScope != "request-retry" || v.OperationRetry != value {
		return v, ErrSourceConflict
	}
	if v.State == "persisted" && (v.OperationState == "persisted" || v.OperationState == "recovered") {
		return v, nil
	}
	if v.OperationState != "unknown" {
		return v, ErrApplyUnknown
	}
	q := configRequest{Action: "commit", SourceID: v.SourceID, Generation: v.Generation, OperationID: id, ExpectedRevision: v.Revision, ExpectedSHA256: v.SHA256}
	result, e := t.configurationAuthority.call(ctx, q)
	if e != nil || result.State != "persisted" || result.Operation.ID != id || result.Operation.Scope != "request-retry" || result.Operation.Retry != value || result.Operation.State != "persisted" {
		return v, ErrApplyUnknown
	}
	return retrySource(result), nil
}
