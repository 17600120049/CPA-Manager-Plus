package cpa

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
)

// H1 is a bounded private protocol, not a general transaction service. Host
// grants are root-owned files; none of these fields are Public API authority.
//
// Config writes carry one allowlisted field intent: Mode "config-field" with the
// canonical Path and scalar Value. Retry/MaxRetryInterval* are never written any
// more; they only keep pre-allowlist H1 parents readable so an operation that was
// already in flight before the cutover can still be reconciled.
type H1Intent struct {
	RequestID    string `json:"requestId"`
	Domain       string `json:"domain"`
	Mode         string `json:"mode"`
	TargetID     string `json:"targetId"`
	KeeperID     string `json:"keeperId"`
	TargetSHA256 string `json:"targetSHA256"`
	KeeperSHA256 string `json:"keeperSHA256"`
	Path         string `json:"path,omitempty"`
	Value        any    `json:"value,omitempty"`
	// Legacy read-only intent fields.
	Retry               int  `json:"retry"`
	MaxRetryInterval    int  `json:"maxRetryInterval,omitempty"`
	MaxRetryIntervalSet bool `json:"maxRetryIntervalSet,omitempty"`
}

// legacyConfigIntent maps a pre-allowlist config intent onto the field it wrote.
func (i H1Intent) legacyConfigIntent() (string, any, bool) {
	switch {
	case i.Domain == "config" && i.Mode == "retry" && !i.MaxRetryIntervalSet && i.MaxRetryInterval == 0:
		return "routing.retry.request-retry", i.Retry, true
	case i.Domain == "config" && i.Mode == "interval" && i.Retry == 0 && i.MaxRetryIntervalSet:
		return "routing.retry.max-retry-interval", i.MaxRetryInterval, true
	default:
		return "", nil, false
	}
}

// fieldIntent returns the field path and value this config intent writes.
func (i H1Intent) fieldIntent() (string, any, bool) {
	if i.Domain != "config" {
		return "", nil, false
	}
	if i.Mode == "config-field" {
		return i.Path, NormalizeConfigValue(i.Value), true
	}
	return i.legacyConfigIntent()
}

func (i H1Intent) parentID() string {
	if i.Domain == "key" {
		return "k:" + i.RequestID
	}
	return "c:" + i.RequestID
}

func (i H1Intent) scopes() []string {
	if i.Domain == "config" {
		if i.Mode == "config-field" {
			return []string{i.Path}
		}
		if i.Mode == "interval" {
			return []string{"max-retry-interval"}
		}
		return []string{"request-retry"}
	}
	if i.Mode == "replace" {
		return []string{"key-add", "key-revoke"}
	}
	return []string{"key-revoke"}
}

// stepScope is the single scope this intent's one step writes. Receipt IDs are
// parent + ":" + "0" + ":" + stepScope, including pre-allowlist config parents.
func (i H1Intent) stepScope() string { return i.scopes()[0] }

func h1ID(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 16 && hex.EncodeToString(b) == s
}

func (i H1Intent) valid() bool {
	if !h1ID(i.RequestID) {
		return false
	}
	if i.Domain == "config" {
		if i.TargetID != "" || i.KeeperID != "" || i.TargetSHA256 != "" || i.KeeperSHA256 != "" {
			return false
		}
		if i.Mode == "config-field" {
			return i.Retry == 0 && i.MaxRetryInterval == 0 && !i.MaxRetryIntervalSet &&
				ValidateConfigFieldValue(i.Path, NormalizeConfigValue(i.Value)) == nil
		}
		path, value, ok := i.legacyConfigIntent()
		return ok && ValidateConfigFieldValue(path, value) == nil
	}
	return i.Domain == "key" && (i.Mode == "replace" || i.Mode == "revoke") && h1ID(i.TargetID) && h1ID(i.KeeperID) && i.TargetID != i.KeeperID && validHex(i.TargetSHA256) && validHex(i.KeeperSHA256) && i.TargetSHA256 != i.KeeperSHA256 && i.Retry == 0 && i.MaxRetryInterval == 0 && !i.MaxRetryIntervalSet && i.Path == "" && i.Value == nil
}

// H1IntentField exposes the single field a config intent writes, for the domain
// service that verifies a Runtime parent against its own journal.
func H1IntentField(i H1Intent) (string, any, bool) { return i.fieldIntent() }

// H1IntentStepScope exposes the one step scope of an intent.
func H1IntentStepScope(i H1Intent) string { return i.stepScope() }

func h1StepID(i H1Intent, n int) string {
	name := i.scopes()[n]
	if name == "key-add" {
		name = "add"
	}
	if name == "key-revoke" {
		name = "revoke"
	}
	return i.parentID() + ":" + strconv.Itoa(n) + ":" + name
}
func h1Digest(v any) string { b, _ := json.Marshal(v); return hashBytes(b) }

type H1Grant struct {
	RunID    string   `json:"runId"`
	Action   string   `json:"action"`
	ParentID string   `json:"parentId"`
	Intent   H1Intent `json:"intent"`
	StepID   string   `json:"stepId"`
	Revision uint64   `json:"revision"`
	SHA256   string   `json:"sha256"`
}
type H1Request struct {
	ParentID       string   `json:"parentId"`
	Intent         H1Intent `json:"intent"`
	StepID         string   `json:"stepId"`
	Anchor         string   `json:"anchor"`
	TerminalDigest string   `json:"terminalDigest"`
}
type H1Step struct {
	ParentID         string `json:"parentId"`
	SourceID         string `json:"sourceId"`
	Generation       string `json:"generation"`
	ID               string `json:"id"`
	Scope            string `json:"scope"`
	State            string `json:"state"`
	BeforeSHA256     string `json:"beforeSHA256"`
	BeginRevision    uint64 `json:"beginRevision"`
	TransitionDigest string `json:"transitionDigest"`
	AfterSHA256      string `json:"afterSHA256"`
	CommitRevision   uint64 `json:"commitRevision"`
	Receipt          string `json:"receipt"`
}
type H1Parent struct {
	ID             string   `json:"id"`
	Intent         H1Intent `json:"intent"`
	Generation     string   `json:"generation"`
	SourceID       string   `json:"sourceId"`
	ClaimSHA256    string   `json:"claimSHA256"`
	ClaimRevision  uint64   `json:"claimRevision"`
	Phase          string   `json:"phase"`
	TerminalDigest string   `json:"terminalDigest"`
	Steps          []H1Step `json:"steps"`
}
type H1Released struct {
	ParentID       string `json:"parentId"`
	Generation     string `json:"generation"`
	SourceID       string `json:"sourceId"`
	SHA256         string `json:"sha256"`
	Revision       uint64 `json:"revision"`
	TerminalDigest string `json:"terminalDigest"`
	Receipts       string `json:"receipts"`
}
type H1Record struct {
	Version         int        `json:"version"`
	RunID           string     `json:"runId"`
	Generation      string     `json:"generation"`
	InitialConsumed string     `json:"initialConsumed"`
	Parent          *H1Parent  `json:"parent"`
	Released        H1Released `json:"released"`
}
