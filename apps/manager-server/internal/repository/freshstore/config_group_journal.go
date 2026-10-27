package freshstore

import (
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
// alias each other.
func (o ConfigGroupOperation) Clone() ConfigGroupOperation {
	copied := o
	if o.Edits != nil {
		copied.Edits = make([]ConfigGroupEdit, len(o.Edits))
		copy(copied.Edits, o.Edits)
	}
	if o.Rules != nil {
		if data, err := json.Marshal(o.Rules); err == nil {
			var rules any
			if json.Unmarshal(data, &rules) == nil {
				copied.Rules = rules
			}
		}
	}
	return copied
}

// loadConfigGroups decodes the group section. Any entry this store cannot prove
// is reported as damage: the page then refuses writes instead of trusting a
// journal it cannot read.
func loadConfigGroups(doc configJournalDoc) (map[string]*ConfigGroupJournal, bool) {
	groups := make(map[string]*ConfigGroupJournal, len(doc.Groups))
	for group, raw := range doc.Groups {
		var journal *ConfigGroupJournal
		if json.Unmarshal(raw, &journal) != nil || !validConfigGroupJournal(group, journal) {
			return nil, true
		}
		groups[group] = journal
	}
	return groups, false
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
			if operation.Code != "" && operation.Code != "superseded" {
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
		next.Generation != old.Generation || !reflect.DeepEqual(next.Edits, old.Edits) ||
		!reflect.DeepEqual(next.Rules, old.Rules) {
		return ErrConflict
	}
	if old.HTTP200 && !next.HTTP200 {
		return ErrConflict
	}
	if next.Code != old.Code {
		if old.State != "unknown" || next.Code == "" {
			return ErrConflict
		}
		if next.State != "failed" && !(next.State == "persisted" && next.Code == "superseded") {
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
	if damaged {
		return nil, true, nil
	}
	return groups, false, nil
}
