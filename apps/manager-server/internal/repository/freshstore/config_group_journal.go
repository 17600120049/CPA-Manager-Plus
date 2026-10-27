package freshstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
)

// The group journal is the Manager-owned record of the gateway configuration
// page. One entry is one save of one page group, and its request ID is the
// Public API operation ID surfaced to the browser. A group save never reserves
// anything in the Runtime: CPA itself answers the write, so the journal records
// intent, the baseline the user confirmed, and the evidence that resolved it.
//
// The container row is shared with the pre-cutover field journals: "fields"
// stays byte-for-byte readable so an operation journaled before the cutover can
// still be reconciled, and the page's groups live beside it under "groups".

// ConfigGroupJournal is the Manager-side journal for one page group.
type ConfigGroupJournal struct {
	Latest     string                          `json:"latest"`
	Operations map[string]ConfigGroupOperation `json:"operations"`
}

// ConfigGroupEdit is one leaf the user edited in a group save. Action "default"
// removes the key so CPA resolves its own default; it never writes that default.
type ConfigGroupEdit struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Value  any    `json:"value,omitempty"`
}

// ConfigGroupOperation is one group save. Target "group" is one PATCH below
// Prefix with only the edited leaves; target "family" is one PUT of a whole
// payload rule family.
//
// State "failed" is terminal: CPA rejected the write and the document is
// unchanged. State "persisted" with Code "superseded" records an acknowledged
// write that another writer has already replaced.
type ConfigGroupOperation struct {
	SourceID   string            `json:"sourceId"`
	Generation string            `json:"generation"`
	RequestID  string            `json:"requestId"`
	Group      string            `json:"group"`
	Target     string            `json:"target"`
	Prefix     string            `json:"prefix"`
	Baseline   string            `json:"baseline"`
	Edits      []ConfigGroupEdit `json:"edits,omitempty"`
	Rules      any               `json:"rules,omitempty"`
	Code       string            `json:"code,omitempty"`
	State      string            `json:"state"`
	HTTP200    bool              `json:"http200,omitempty"`
}

// Clone returns a deep copy, so a verbatim raw entry and a decoded one never
// alias each other. Rules go through a decoder that keeps json.Number, so a
// 64-bit integer in a rule parameter is not rounded on the way to the copy.
func (o ConfigGroupOperation) Clone() ConfigGroupOperation {
	copied := o
	if o.Edits != nil {
		copied.Edits = make([]ConfigGroupEdit, len(o.Edits))
		copy(copied.Edits, o.Edits)
	}
	if o.Rules != nil {
		if data, err := json.Marshal(o.Rules); err == nil {
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			var rules any
			if decoder.Decode(&rules) == nil {
				copied.Rules = rules
			}
		}
	}
	return copied
}

// loadConfigGroups decodes the group section. Any entry this store cannot prove
// is reported with its exact bytes: the page then refuses writes instead of
// trusting a journal it cannot read, and no later write of another section may
// discard an original operation that is still unresolved.
func loadConfigGroups(doc configJournalDoc) (map[string]*ConfigGroupJournal, map[string]json.RawMessage) {
	groups := make(map[string]*ConfigGroupJournal, len(doc.Groups))
	var damaged map[string]json.RawMessage
	for group, raw := range doc.Groups {
		journal, err := decodeConfigGroupJournal(raw)
		if err != nil || !validConfigGroupJournal(group, journal) {
			if damaged == nil {
				damaged = map[string]json.RawMessage{}
			}
			damaged[group] = raw
			continue
		}
		normalizeConfigGroupJournal(journal)
		groups[group] = journal
	}
	// A damaged entry is reported beside the groups this store can prove. Dropping
	// the valid ones would erase their unresolved original operations on the next
	// write of any other section, which is exactly the harm the damaged bytes are
	// preserved to prevent. The caller still treats the page as unprovable, so no
	// write may proceed; only durability of the whole map is at stake here.
	return groups, damaged
}

// decodeConfigGroupJournal decodes one entry keeping numbers as json.Number. A
// rule parameter can hold a 64-bit integer, and a plain float64 decode would
// change its low digits between the value the page shows and the value it sends
// back. json.Number also marshals to the original literal, so the entry is
// re-emitted exactly.
func decodeConfigGroupJournal(raw json.RawMessage) (*ConfigGroupJournal, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var journal *ConfigGroupJournal
	if decoder.Decode(&journal) != nil {
		return nil, ErrConflict
	}
	return journal, nil
}

// normalizeConfigGroupJournal converts JSON numbers back into the exact scalar
// types the service validated, so the append-only comparison never depends on
// the float64 JSON decoding produces. Without it a group save that carries a
// number compares its in-memory int against a reloaded float64, fails, and leaves
// a write CPA already applied locked behind a conflict.
func normalizeConfigGroupJournal(journal *ConfigGroupJournal) {
	if journal == nil {
		return
	}
	for id, operation := range journal.Operations {
		for index := range operation.Edits {
			operation.Edits[index].Value = normalizeGroupValue(operation.Edits[index].Value)
		}
		operation.Rules = normalizeRuleValue(operation.Rules)
		journal.Operations[id] = operation
	}
}

// normalizeRuleValue keeps every rule parameter as json.Number. A rule value is
// arbitrary and can carry a 64-bit integer, so it must never become an int (which
// would truncate) or a float64 (which would round). The literals are re-emitted
// from the numbers, and canonicalEqual hashes the canonical form on both sides.
func normalizeRuleValue(value any) any {
	switch typed := value.(type) {
	case []any:
		for index := range typed {
			typed[index] = normalizeRuleValue(typed[index])
		}
		return typed
	case map[string]any:
		for key := range typed {
			typed[key] = normalizeRuleValue(typed[key])
		}
		return typed
	case float64:
		// A rule the store decoded without UseNumber; json.Number from the
		// literal is the best exact form still available.
		if literal, err := json.Marshal(typed); err == nil {
			return json.Number(literal)
		}
		return typed
	default:
		return value
	}
}

// normalizeGroupValue rewrites every whole JSON number in a decoded value to an
// int and rebuilds the containers around it. A number that arrived as
// json.Number is already exact and is left alone; one that did not survive as a
// whole int is kept so validation can refuse it.
func normalizeGroupValue(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if number, ok := operationInt(typed); ok {
			return number
		}
		return typed
	case float64:
		if number, ok := operationInt(typed); ok {
			return number
		}
		return typed
	case []any:
		for index := range typed {
			typed[index] = normalizeGroupValue(typed[index])
		}
		return typed
	case map[string]any:
		for key := range typed {
			typed[key] = normalizeGroupValue(typed[key])
		}
		return typed
	default:
		return value
	}
}

// validConfigGroupJournal is the structural contract of one group's journal.
// Path membership, value shapes and the product rules are owned by the service
// that wrote the operation; the store guarantees coherence and immutability.
func validConfigGroupJournal(group string, journal *ConfigGroupJournal) bool {
	if journal == nil || group == "" {
		return false
	}
	if _, ok := journal.Operations[journal.Latest]; !ok {
		return false
	}
	for id, operation := range journal.Operations {
		if !hashHex(id, 16) || operation.RequestID != id || operation.Group != group ||
			len(operation.SourceID) < 16 || operation.Generation == "" || !hashHex(operation.Baseline, 32) {
			return false
		}
		switch operation.Target {
		case "group":
			if operation.Prefix == "" || len(operation.Edits) == 0 || operation.Rules != nil {
				return false
			}
			for _, edit := range operation.Edits {
				if edit.Path == "" {
					return false
				}
				switch edit.Action {
				case "set":
					if edit.Value == nil {
						return false
					}
				case "default":
					if edit.Value != nil {
						return false
					}
				default:
					return false
				}
			}
		case "family":
			if operation.Prefix == "" || operation.Edits != nil || operation.Rules == nil {
				return false
			}
		default:
			return false
		}
		switch operation.State {
		case "unknown":
			if operation.Code != "" {
				return false
			}
		case "persisted":
			// "superseded" records an acknowledged write another writer replaced;
			// "partial" records a save only part of whose instructions landed, so
			// the page must never claim the document is unchanged.
			if operation.Code != "" && operation.Code != "superseded" && operation.Code != "partial" {
				return false
			}
		case "failed":
			if operation.Code == "" || operation.HTTP200 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// validateConfigGroupJournalUpdate enforces append-only history: a CAS callback
// may add an operation and advance an original one, and can never rewrite,
// replay or erase what it already recorded.
func validateConfigGroupJournalUpdate(before, after map[string]*ConfigGroupJournal) error {
	for group, oldJournal := range before {
		if oldJournal == nil {
			continue
		}
		nextJournal := after[group]
		if nextJournal == nil {
			return ErrConflict
		}
		for id, old := range oldJournal.Operations {
			next, ok := nextJournal.Operations[id]
			if !ok {
				return ErrConflict
			}
			if err := validateConfigGroupOperationUpdate(old, next); err != nil {
				return err
			}
		}
		if oldJournal.Latest != "" {
			if _, ok := nextJournal.Operations[oldJournal.Latest]; !ok {
				return ErrConflict
			}
			// An unknown original stays the named latest until it is resolved.
			if oldJournal.Operations[oldJournal.Latest].State == "unknown" && nextJournal.Latest != oldJournal.Latest {
				return ErrConflict
			}
		}
	}
	for group, journal := range after {
		if before[group] != nil {
			continue
		}
		// A brand-new group journal arrives as one complete, valid entry.
		if !validConfigGroupJournal(group, journal) {
			return ErrConflict
		}
	}
	return nil
}

func validateConfigGroupOperationUpdate(old, next ConfigGroupOperation) error {
	if next.RequestID != old.RequestID || next.Group != old.Group || next.Target != old.Target ||
		next.Prefix != old.Prefix || next.Baseline != old.Baseline || next.SourceID != old.SourceID ||
		next.Generation != old.Generation || !journalEqual(next.Edits, old.Edits) ||
		!journalEqual(next.Rules, old.Rules) {
		return ErrConflict
	}
	if old.HTTP200 && !next.HTTP200 {
		return ErrConflict
	}
	if next.Code != old.Code {
		if old.State != "unknown" || next.Code == "" {
			return ErrConflict
		}
		if next.State != "failed" && !(next.State == "persisted" && (next.Code == "superseded" || next.Code == "partial")) {
			return ErrConflict
		}
	}
	if next.State == old.State {
		return nil
	}
	if old.State != "unknown" || (next.State != "persisted" && next.State != "failed") {
		return ErrConflict
	}
	if next.State == "failed" && (next.Code == "" || next.HTTP200) {
		return ErrConflict
	}
	return nil
}

// journalEqual compares two journal values by their canonical JSON. A value that
// crossed the JSON boundary and one that did not encode identically — 3 and
// json.Number("3") are the same edit — so the append-only check must not read a
// re-encoded value as a rewrite.
func journalEqual(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return bytes.Equal(left, right)
}

// cloneConfigGroupJournals deep-copies the group map for a CAS before-image.
func cloneConfigGroupJournals(source map[string]*ConfigGroupJournal) map[string]*ConfigGroupJournal {
	if source == nil {
		return map[string]*ConfigGroupJournal{}
	}
	out := make(map[string]*ConfigGroupJournal, len(source))
	for group, journal := range source {
		if journal == nil {
			out[group] = nil
			continue
		}
		copied := &ConfigGroupJournal{Latest: journal.Latest, Operations: make(map[string]ConfigGroupOperation, len(journal.Operations))}
		for id, operation := range journal.Operations {
			copied.Operations[id] = operation.Clone()
		}
		out[group] = copied
	}
	return out
}

// ValidateConfigGroupJournals is the read-side guard for the page: one group may
// hold an unknown original while another group keeps saving, but a journal this
// store cannot prove locks the whole page.
func ValidateConfigGroupJournals(ctx context.Context, q queryer) (map[string]*ConfigGroupJournal, bool, error) {
	var data string
	err := q.QueryRowContext(ctx, "select value from settings where key=?", configJournalKey).Scan(&data)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return map[string]*ConfigGroupJournal{}, false, nil
	case err != nil:
		return nil, true, err
	}
	var doc configJournalDoc
	if json.Unmarshal([]byte(data), &doc) != nil {
		return nil, true, nil
	}
	groups, damaged := loadConfigGroups(doc)
	if damaged != nil {
		return nil, true, nil
	}
	return groups, false, nil
}
