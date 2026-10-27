package cpa

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// ConfigWriteError is CPA's own verdict on a configuration write. CPA parses,
// validates and only then writes the file, so a definite rejection means the
// stored document is unchanged and the draft is still the user's to fix.
type ConfigWriteError struct {
	Status  int
	Code    string
	Field   string
	Message string
}

func (e ConfigWriteError) Error() string {
	if e.Field != "" {
		return e.Code + ":" + e.Field
	}
	return e.Code
}

// Definite reports a 4xx CPA answered identically every time. Network failures,
// timeouts and 5xx stay unknown: the write may still have landed.
func (e ConfigWriteError) Definite() bool {
	if e.Status < 400 || e.Status >= 500 {
		return false
	}
	switch e.Status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return false
	}
	return e.Status == http.StatusBadRequest || e.Status == http.StatusUnprocessableEntity ||
		e.Status == http.StatusNotFound || e.Status == http.StatusConflict
}

// maxConfigMessage bounds the diagnostic text CPAMP keeps from CPA.
const maxConfigMessage = 240

// ReadConfigDocument reads the whole configuration once. The document carries
// provider and client secrets, so it NEVER leaves the Manager: callers project
// the scoped leaves and rules they may show and drop everything else.
func (t *Transport) ReadConfigDocument(ctx context.Context, management string) (map[string]any, error) {
	if !validManagementKey(management) {
		return nil, ErrProofUnavailable
	}
	code, body, err := t.request(ctx, http.MethodGet, "/v8/management/config", management, nil)
	if err != nil {
		return nil, ErrProofUnavailable
	}
	if code != http.StatusOK {
		return nil, ErrProofUnavailable
	}
	var document map[string]any
	if json.Unmarshal(body, &document) != nil || document == nil {
		return nil, ErrSourceConflict
	}
	return document, nil
}

// PatchConfigPath merges edited leaves below one group prefix. CPA merges
// mappings recursively, keeps explicit nulls and replaces arrays by value.
func (t *Transport) PatchConfigPath(ctx context.Context, management, prefix string, payload []byte) error {
	if !validManagementKey(management) || prefix == "" || len(payload) == 0 {
		return ErrProofUnavailable
	}
	return t.writeConfigPath(ctx, http.MethodPatch, prefix, management, payload)
}

// PutConfigValue replaces one whole value, used for a payload rule family.
func (t *Transport) PutConfigValue(ctx context.Context, management, path string, payload []byte) error {
	if !validManagementKey(management) || path == "" || len(payload) == 0 {
		return ErrProofUnavailable
	}
	return t.writeConfigPath(ctx, http.MethodPut, path, management, payload)
}

// DeleteConfigPath restores a leaf to CPA's own default by removing the key.
func (t *Transport) DeleteConfigPath(ctx context.Context, management, path string) error {
	if !validManagementKey(management) || path == "" {
		return ErrProofUnavailable
	}
	return t.writeConfigPath(ctx, http.MethodDelete, path, management, nil)
}

func (t *Transport) writeConfigPath(ctx context.Context, method, path, management string, payload []byte) error {
	endpoint := configPathEndpoint(path)
	code, body, err := t.request(ctx, method, endpoint, management, payload)
	if err != nil {
		return ErrApplyUnknown
	}
	switch code {
	case http.StatusOK, http.StatusNoContent:
		return nil
	}
	rejection := parseConfigWriteError(code, body)
	if rejection.Definite() {
		return rejection
	}
	return ErrApplyUnknown
}

// parseConfigWriteError keeps only stable codes and redacted diagnostics. A CPA
// validation message can quote the offending YAML value, which may be a secret,
// so every quoted literal is removed before the text can reach a browser.
func parseConfigWriteError(status int, body []byte) ConfigWriteError {
	rejection := ConfigWriteError{Status: status}
	if status == http.StatusNotFound {
		rejection.Code = "not_found"
		return rejection
	}
	var parsed struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Field   string `json:"field"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		rejection.Code = "invalid_config"
		return rejection
	}
	rejection.Code = parsed.Error
	if rejection.Code == "" {
		rejection.Code = "invalid_config"
	}
	rejection.Field = parsed.Field
	rejection.Message = sanitizeConfigMessage(parsed.Message)
	return rejection
}

// sanitizeConfigMessage drops quoted literals, control characters and excess
// length from an upstream diagnostic.
func sanitizeConfigMessage(message string) string {
	var out strings.Builder
	var quote rune
	for _, character := range message {
		if quote != 0 {
			if character == quote {
				quote = 0
			}
			continue
		}
		switch character {
		case '\'', '"', '`':
			quote = character
			continue
		}
		if character < 0x20 || character == 0x7f {
			out.WriteByte(' ')
			continue
		}
		out.WriteRune(character)
	}
	text := strings.Join(strings.Fields(out.String()), " ")
	if len(text) > maxConfigMessage {
		text = text[:maxConfigMessage]
	}
	return text
}

// ConfigPresence distinguishes a missing key from an explicit null: the page
// shows both, because CPA resolves them differently.
type ConfigPresence string

const (
	ConfigPresent ConfigPresence = "present"
	ConfigMissing ConfigPresence = "missing"
	ConfigNull    ConfigPresence = "null"
)

// LookupConfigValue resolves one dotted path inside a read document.
func LookupConfigValue(document map[string]any, path string) (any, ConfigPresence) {
	var current any = document
	for _, part := range strings.Split(path, ".") {
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil, ConfigMissing
		}
		current, ok = mapping[part]
		if !ok {
			return nil, ConfigMissing
		}
	}
	if current == nil {
		return nil, ConfigNull
	}
	return current, ConfigPresent
}

// ConfigSubtree resolves the mapping below one dotted path, if any.
func ConfigSubtree(document map[string]any, path string) (map[string]any, bool) {
	value, presence := LookupConfigValue(document, path)
	if presence != ConfigPresent {
		return nil, false
	}
	mapping, ok := value.(map[string]any)
	return mapping, ok
}
