package cpa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
)

// ErrManagementRejected is CPA's own 401 to an External management request: the
// credential this installation persisted no longer authenticates. It is a typed
// blocker, never a retry reason. An External CPA is administered by the user, so
// the only way back is the user rebinding the connection; guessing, resending or
// rebinding automatically would write to a CPA this Manager no longer proves it
// owns.
var ErrManagementRejected = errors.New("external cpa management credential rejected")

// ErrExternalTargetMoved reports a committed External target that no longer names
// the connection this Manager holds: the address the user configured changed after
// the installation was committed. It is a typed blocker, not an unavailable
// target — the installation exists — and only a rebind can reach it again, so no
// surface may treat it as a retry or as a missing proof.
var ErrExternalTargetMoved = errors.New("external cpa target moved")

// ExternalKeySnapshot is the user's CPA client-key list together with the digest
// of the configuration that list belongs to. An External CPA owns its own
// posture, key naming and extra credentials, so the only facts this Manager may
// compare are membership and the whole-document digest: there is no managed
// template to prove here.
type ExternalKeySnapshot struct {
	Keys         []string
	SourceSHA256 string
}

// ExternalKeySnapshot reads the External CPA client-key list and the config
// digest. Both reads authenticate with the persisted management key; a 401 is
// the typed ErrManagementRejected blocker rather than an unavailable proof.
func (t *Transport) ExternalKeySnapshot(ctx context.Context, management string) (ExternalKeySnapshot, error) {
	var snapshot ExternalKeySnapshot
	if t == nil || !validManagementKey(management) {
		return snapshot, ErrProofUnavailable
	}
	code, body, err := t.request(ctx, http.MethodGet, "/v8/management/config/access/api-keys", management, nil)
	if err != nil {
		return snapshot, ErrProofUnavailable
	}
	switch code {
	case http.StatusUnauthorized:
		return snapshot, ErrManagementRejected
	case http.StatusOK:
	default:
		return snapshot, ErrSourceConflict
	}
	if json.Unmarshal(body, &snapshot.Keys) != nil {
		return snapshot, ErrSourceConflict
	}
	for _, key := range snapshot.Keys {
		if !validExternalKey(key) {
			return snapshot, ErrSourceConflict
		}
	}
	code, body, err = t.request(ctx, http.MethodGet, "/v8/management/config.yaml", management, nil)
	if err != nil {
		return snapshot, ErrProofUnavailable
	}
	switch code {
	case http.StatusUnauthorized:
		return snapshot, ErrManagementRejected
	case http.StatusOK:
	default:
		return snapshot, ErrSourceConflict
	}
	digest := sha256.Sum256(body)
	snapshot.SourceSHA256 = hex.EncodeToString(digest[:])
	return snapshot, nil
}

// ExternalWriteKeySet writes the whole client-key list of an External CPA. It
// sends exactly one request and never retries: an acknowledged 401 is the typed
// blocker, and anything else leaves the effect unknown for the caller's
// read-back, exactly like the managed destination.
func (t *Transport) ExternalWriteKeySet(ctx context.Context, management string, keys []string) error {
	if t == nil || !validManagementKey(management) || len(keys) == 0 {
		return ErrSourceConflict
	}
	for i, key := range keys {
		if !validExternalKey(key) || slices.Contains(keys[:i], key) {
			return ErrSourceConflict
		}
	}
	body, err := json.Marshal(keys)
	if err != nil {
		return ErrProofUnavailable
	}
	code, _, err := t.request(ctx, http.MethodPut, "/v8/management/config/access/api-keys", management, body)
	if err != nil {
		return ErrApplyUnknown
	}
	switch code {
	case http.StatusOK, http.StatusNoContent:
		return nil
	case http.StatusUnauthorized:
		return ErrManagementRejected
	}
	return ErrApplyUnknown
}

// ExternalSampleKeySet observes the External CPA's own client authentication for
// the keys one step added or removed. Only the listed keys are probed: an
// External CPA may run with client authentication disabled, and this Manager
// does not own that setting.
func (t *Transport) ExternalSampleKeySet(ctx context.Context, accepted, rejected []string) (bool, error) {
	if t == nil {
		return false, ErrProofUnavailable
	}
	for _, sample := range []struct {
		keys   []string
		status int
	}{{accepted, http.StatusOK}, {rejected, http.StatusUnauthorized}} {
		for _, key := range sample.keys {
			code, _, err := t.request(ctx, http.MethodGet, "/v1/models", key, nil)
			if err != nil {
				return false, err
			}
			// A client key that is not live yet is an unproven effect, not a
			// management credential failure: only a management request proves
			// that, and this sample authenticates as a client.
			if code != sample.status {
				return false, nil
			}
		}
	}
	return true, nil
}

// validExternalKey accepts the client credentials a CPA v8 configuration stores:
// a printable, non-empty token. CPA itself mints arbitrary shapes, so this guard
// bounds what may be echoed into a write body; it never rewrites a value.
func validExternalKey(key string) bool {
	if key == "" || len(key) > 4096 {
		return false
	}
	for _, c := range key {
		if c < 0x21 || c == 0x7f {
			return false
		}
	}
	return true
}
