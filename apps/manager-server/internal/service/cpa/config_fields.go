package cpa

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// ErrInvalidConfigField rejects anything outside the allowlist: unknown paths,
// wrong scalar types and out-of-range integers. Manager-side validation is the
// first gate; CPA still answers 400 for an unknown YAML path and 422 for a type
// error (internal/api/handlers/management/config_v8.go:118-186, v8.0.13).
var ErrInvalidConfigField = errors.New("invalid config field")

type ConfigFieldType string

const (
	ConfigFieldInt  ConfigFieldType = "int"
	ConfigFieldBool ConfigFieldType = "bool"
)

// ConfigField is one editable CPA YAML leaf. Path is the canonical dotted CPA
// path; Canonical scope, H1 step scope and resource path are all derived from
// it. Min/Max are inclusive and only apply to ConfigFieldInt.
type ConfigField struct {
	Path string
	Type ConfigFieldType
	Min  int
	Max  int
}

// configFields is the ONLY Manager-side write allowlist. Every field is written
// as its own H1 operation with its own journal entry; no field here is a full
// YAML document, a section, a credential or a secret.
//
// Types and non-positive semantics are frozen from CPA v8.0.13 (tag d7914af):
//   - routing.retry.request-retry        internal/config/config.go:86-88 (int, <=0 disables extra rounds)
//   - routing.retry.max-retry-interval   internal/config/config.go:93-97 (int seconds, non-positive forbids waits)
//   - requests.passthrough-headers       internal/config/sdk_config.go:66-67 (bool, default false)
//   - requests.nonstream-keepalive-interval internal/config/sdk_config.go:71-74 (int seconds, <=0 disables)
//   - requests.streaming.keepalive-seconds  internal/config/sdk_config.go:100-104 (int seconds, <=0 disables)
//   - requests.streaming.bootstrap-retries  internal/config/sdk_config.go:106-109 (int count, <=0 disables)
//
// CPAMP adds conservative input bounds the upstream parser does not enforce:
// keep-alive intervals 0-300 seconds and retry counts 0-10. A missing key and an
// explicit null both mean "upstream default / disabled"; the Manager never
// writes null and never deletes a key.
var configFields = map[string]ConfigField{
	"routing.retry.request-retry":           {Path: "routing.retry.request-retry", Type: ConfigFieldInt, Min: 0, Max: 10},
	"routing.retry.max-retry-interval":      {Path: "routing.retry.max-retry-interval", Type: ConfigFieldInt, Min: 0, Max: 300},
	"requests.streaming.keepalive-seconds":  {Path: "requests.streaming.keepalive-seconds", Type: ConfigFieldInt, Min: 0, Max: 300},
	"requests.streaming.bootstrap-retries":  {Path: "requests.streaming.bootstrap-retries", Type: ConfigFieldInt, Min: 0, Max: 10},
	"requests.nonstream-keepalive-interval": {Path: "requests.nonstream-keepalive-interval", Type: ConfigFieldInt, Min: 0, Max: 300},
	"requests.passthrough-headers":          {Path: "requests.passthrough-headers", Type: ConfigFieldBool},
}

// LookupConfigField is the single membership test for the allowlist.
func LookupConfigField(path string) (ConfigField, bool) {
	field, ok := configFields[path]
	return field, ok
}

// ConfigFieldPaths returns the allowlist in a stable order for deterministic
// journal writes and tests.
func ConfigFieldPaths() []string {
	paths := make([]string, 0, len(configFields))
	for path := range configFields {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// ValidateConfigFieldValue accepts only the allowlisted scalar type and range.
func ValidateConfigFieldValue(path string, value any) error {
	field, ok := LookupConfigField(path)
	if !ok {
		return ErrInvalidConfigField
	}
	switch field.Type {
	case ConfigFieldInt:
		number, ok := value.(int)
		if !ok || number < field.Min || number > field.Max {
			return ErrInvalidConfigField
		}
	case ConfigFieldBool:
		if _, ok := value.(bool); !ok {
			return ErrInvalidConfigField
		}
	default:
		return ErrInvalidConfigField
	}
	return nil
}

// NormalizeConfigValue converts an integral JSON number back into the exact Go
// scalar the allowlist uses, so a value that crossed the Runtime JSON boundary
// still compares and validates identically. Non-integral numbers are kept and
// then rejected by validation.
func NormalizeConfigValue(value any) any {
	if number, ok := value.(float64); ok && number == float64(int(number)) {
		return int(number)
	}
	return value
}

// ParseConfigFieldValue decodes one exact JSON scalar. Objects, arrays, null,
// strings, floats and trailing data are all rejected.
func ParseConfigFieldValue(path string, raw json.RawMessage) (any, error) {
	field, ok := LookupConfigField(path)
	if !ok || len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, ErrInvalidConfigField
	}
	switch field.Type {
	case ConfigFieldInt:
		var number int
		if len(raw) == 0 || json.Unmarshal(raw, &number) != nil {
			return nil, ErrInvalidConfigField
		}
		if ValidateConfigFieldValue(path, number) != nil {
			return nil, ErrInvalidConfigField
		}
		return number, nil
	case ConfigFieldBool:
		var flag bool
		if len(raw) == 0 || json.Unmarshal(raw, &flag) != nil {
			return nil, ErrInvalidConfigField
		}
		return flag, nil
	default:
		return nil, ErrInvalidConfigField
	}
}

// EncodeConfigFieldValue renders the exact JSON scalar CPA's config endpoint
// accepts. It never emits YAML, null or a composite value.
func EncodeConfigFieldValue(path string, value any) ([]byte, error) {
	if err := ValidateConfigFieldValue(path, value); err != nil {
		return nil, err
	}
	switch number := value.(type) {
	case int:
		return []byte(strconv.Itoa(number)), nil
	case bool:
		if number {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	default:
		return nil, ErrInvalidConfigField
	}
}

// ConfigFieldEndpoint is the single CPA management endpoint for a field.
// CPA reads a missing key as 404 and rejects an unknown path as 400.
func ConfigFieldEndpoint(path string) (string, error) {
	if _, ok := LookupConfigField(path); !ok {
		return "", ErrInvalidConfigField
	}
	return "/v8/management/config/" + strings.ReplaceAll(path, ".", "/"), nil
}

// ConfigFieldResource is the Public API resource suffix for one field.
func ConfigFieldResource(path string) string {
	return strings.ReplaceAll(path, ".", "/")
}
