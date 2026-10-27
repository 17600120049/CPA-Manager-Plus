package privateconfig

import (
	"encoding/json"
	"strconv"
	"strings"
)

// configFields mirrors the Manager write allowlist for the Runtime journal. The
// Host validates independently: a Manager bug must never widen the file writer,
// and a path outside this table is rejected even if a grant names it.
// Types and non-positive semantics are frozen from CPA v8.0.13 (tag d7914af):
// routing.retry.request-retry (config.go:86-88), routing.retry.max-retry-interval
// (config.go:93-97), requests.passthrough-headers (sdk_config.go:66-67),
// requests.nonstream-keepalive-interval (sdk_config.go:71-74),
// requests.streaming.keepalive-seconds (sdk_config.go:100-104) and
// requests.streaming.bootstrap-retries (sdk_config.go:106-109).
var configFields = map[string]fieldSpec{
	"routing.retry.request-retry":           {kind: "int", min: 0, max: 10},
	"routing.retry.max-retry-interval":      {kind: "int", min: 0, max: 300},
	"requests.streaming.keepalive-seconds":  {kind: "int", min: 0, max: 300},
	"requests.streaming.bootstrap-retries":  {kind: "int", min: 0, max: 10},
	"requests.nonstream-keepalive-interval": {kind: "int", min: 0, max: 300},
	"requests.passthrough-headers":          {kind: "bool"},
}

type fieldSpec struct {
	kind string
	min  int
	max  int
}

func fieldFor(path string) ([]string, fieldSpec, bool) {
	spec, ok := configFields[path]
	if !ok {
		return nil, fieldSpec{}, false
	}
	return strings.Split(path, "."), spec, true
}

// normalizeFieldValue turns a JSON-decoded scalar into the exact Go type the
// allowlist uses; a non-integral number stays float64 and is then rejected.
func normalizeFieldValue(value any) any {
	switch number := value.(type) {
	case float64:
		if number == float64(int(number)) {
			return int(number)
		}
	case int64:
		return int(number)
	}
	return value
}

func validFieldValue(path string, value any) bool {
	_, spec, ok := fieldFor(path)
	if !ok {
		return false
	}
	switch scalar := normalizeFieldValue(value).(type) {
	case int:
		return spec.kind == "int" && scalar >= spec.min && scalar <= spec.max
	case bool:
		return spec.kind == "bool"
	default:
		return false
	}
}

// fieldLeaf renders the exact YAML scalar a field value must produce.
func fieldLeaf(path string, value any) (tag, text string, ok bool) {
	if !validFieldValue(path, value) {
		return "", "", false
	}
	switch scalar := normalizeFieldValue(value).(type) {
	case int:
		return "!!int", strconv.Itoa(scalar), true
	case bool:
		if scalar {
			return "!!bool", "true", true
		}
		return "!!bool", "false", true
	default:
		return "", "", false
	}
}

// operationPath is the canonical field an operation writes, including records
// reserved before the allowlist cutover.
func operationPath(op Operation) (string, any, bool) {
	if op.Path != "" {
		value := normalizeFieldValue(op.Value)
		return op.Path, value, validFieldValue(op.Path, value)
	}
	switch op.Scope {
	case "request-retry":
		return "routing.retry.request-retry", op.Retry, true
	case "max-retry-interval":
		return "routing.retry.max-retry-interval", op.MaxRetryInterval, true
	default:
		return "", nil, false
	}
}

// validOperation accepts the pre-allowlist scopes of records written before the
// cutover and the generic config-field scope of every new reservation.
func validOperation(r *record) bool {
	op := r.Operation
	if op.ID == "" || op.Generation == "" || !operationScopeValid(op.Scope) {
		return false
	}
	switch op.Scope {
	case "initial-key":
		return true
	case "key-add", "key-revoke":
		return r.Version == 2
	}
	if op.Path != "" {
		return op.Path == op.Scope && validFieldValue(op.Path, op.Value)
	}
	// Pre-allowlist record: the value lives in the legacy scalars.
	path, value, ok := operationPath(op)
	return ok && validFieldValue(path, value) && op.Retry == 0 && op.MaxRetryInterval == 0 && !op.MaxRetryIntervalSet
}

func operationScopeValid(scope string) bool {
	switch scope {
	case "initial-key", "key-add", "key-revoke":
		return true
	}
	_, ok := configFields[scope]
	return ok
}

// committedFieldValues merges the generic committed map with the pre-allowlist
// committed scalars into one path -> JSON scalar map.
func committedFieldValues(r *record) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for path, raw := range r.CommittedFields {
		out[path] = raw
	}
	if r.CommittedRetry != nil {
		raw, _ := json.Marshal(*r.CommittedRetry)
		out["routing.retry.request-retry"] = raw
	}
	if r.CommittedMaxRetryInterval != nil {
		raw, _ := json.Marshal(*r.CommittedMaxRetryInterval)
		out["routing.retry.max-retry-interval"] = raw
	}
	return out
}
