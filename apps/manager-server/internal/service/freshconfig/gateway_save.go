package freshconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

// Expected is the baseline the user confirmed before saving. The Manager re-reads
// the installation before writing and refuses a save whose baseline moved. CPA
// writes unconditionally, so this is a best-effort check and never claims to be
// an atomic compare-and-swap.
type Expected struct {
	Generation string `json:"generation"`
	Baseline   string `json:"baseline"`
}

// Edit is one leaf the user changed. Action "set" writes the value; action
// "default" removes the key so CPA resolves its own default.
type Edit struct {
	Path   string          `json:"path"`
	Action string          `json:"action"`
	Value  json.RawMessage `json:"value"`
}

// GroupRequest saves exactly one page group.
type GroupRequest struct {
	RequestID string   `json:"requestId"`
	Expected  Expected `json:"expected"`
	Edits     []Edit   `json:"edits"`
}

// FamilyRequest saves exactly one whole payload rule family.
type FamilyRequest struct {
	RequestID string          `json:"requestId"`
	Expected  Expected        `json:"expected"`
	Rules     json.RawMessage `json:"rules"`
}

// canonicalEqual compares two values by their canonical JSON, so a value that
// crossed the journal's JSON boundary still equals the one that was validated.
func canonicalEqual(a, b any) bool { return canonicalDigest(a) == canonicalDigest(b) }

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// validateEdits accepts only a well-shaped request for this group: a known leaf,
// a shape the leaf can carry, no duplicate path and the two product rules CPAMP
// owns. Value bounds stay CPA's.
func validateEdits(group cpa.ConfigGroup, edits []Edit) ([]freshstore.ConfigGroupEdit, error) {
	if len(edits) == 0 || len(edits) > len(group.Leaves) {
		return nil, ErrInvalid
	}
	members := make(map[string]bool, len(group.Leaves))
	for _, path := range group.Leaves {
		members[path] = true
	}
	out := make([]freshstore.ConfigGroupEdit, 0, len(edits))
	seen := map[string]bool{}
	for _, edit := range edits {
		if !members[edit.Path] || seen[edit.Path] {
			return nil, ErrInvalid
		}
		seen[edit.Path] = true
		switch edit.Action {
		case "default":
			if len(edit.Value) != 0 {
				return nil, ErrInvalid
			}
			out = append(out, freshstore.ConfigGroupEdit{Path: edit.Path, Action: "default"})
		case "set":
			if len(edit.Value) == 0 {
				return nil, ErrInvalid
			}
			value, err := cpa.ParseConfigLeafValue(edit.Path, edit.Value)
			if err != nil {
				return nil, ErrInvalid
			}
			if edit.Path == "requests.proxy-url" {
				if err = cpa.ValidateProxyURL(value); err != nil {
					return nil, ErrInvalid
				}
			}
			out = append(out, freshstore.ConfigGroupEdit{Path: edit.Path, Action: "set", Value: value})
		default:
			return nil, ErrInvalid
		}
	}
	return out, nil
}

// ValidateGroupRequest rejects everything the page contract does not allow
// before any journal or CPA write happens.
func ValidateGroupRequest(groupID string, q GroupRequest) error {
	group, ok := cpa.LookupConfigGroup(groupID)
	if !ok {
		return ErrInvalid
	}
	if err := validateExpected(q.Expected); err != nil {
		return err
	}
	if !freshkeysValidRequestID(q.RequestID) {
		return ErrInvalid
	}
	if _, err := validateEdits(group, q.Edits); err != nil {
		return err
	}
	return nil
}

// ValidateFamilyRequest rejects a rule request before any write happens.
func ValidateFamilyRequest(family string, q FamilyRequest) error {
	entry, ok := cpa.LookupConfigRuleFamily(family)
	if !ok {
		return ErrInvalid
	}
	if err := validateExpected(q.Expected); err != nil {
		return err
	}
	if !freshkeysValidRequestID(q.RequestID) {
		return ErrInvalid
	}
	if _, err := cpa.ParseConfigRules(entry.Family, q.Rules); err != nil {
		return ErrInvalid
	}
	return nil
}

func validateExpected(expected Expected) error {
	if expected.Generation == "" || len(expected.Generation) > 64 || !isHex(expected.Baseline, 32) {
		return ErrInvalid
	}
	return nil
}

func isHex(value string, size int) bool {
	if value != strings.ToLower(value) || len(value) != size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// SaveGroup performs exactly one group save. The journal entry is durable before
// the first CPA write, so a lost response is resolved by reading back.
func (s *Service) SaveGroup(ctx context.Context, groupID string, q GroupRequest) (freshstore.ConfigGroupOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, ok := cpa.LookupConfigGroup(groupID)
	if !ok {
		return freshstore.ConfigGroupOperation{}, ErrInvalid
	}
	edits, err := validateEdits(group, q.Edits)
	if err != nil || validateExpected(q.Expected) != nil || !freshkeysValidRequestID(q.RequestID) {
		return freshstore.ConfigGroupOperation{}, ErrInvalid
	}
	r, err := s.store.Load(ctx)
	if err != nil {
		return freshstore.ConfigGroupOperation{}, err
	}
	auth, err := s.resolve(ctx, r)
	if err != nil || auth.wire == nil {
		return freshstore.ConfigGroupOperation{}, cpa.ErrProofUnavailable
	}
	if !auth.capable.Writable {
		return freshstore.ConfigGroupOperation{}, ErrReadOnly
	}
	if r.ConfigGroupsInvalid {
		return freshstore.ConfigGroupOperation{}, cpa.ErrSourceConflict
	}
	if entry := r.ConfigGroups[groupID]; entry != nil {
		if old, exists := entry.Operations[q.RequestID]; exists {
			// Tombstone forever: an existing request ID is never a new write.
			if !sameGroupRequest(old, edits, q.Expected.Baseline) {
				return old, cpa.ErrSourceConflict
			}
			return old, nil
		}
		if latest, exists := entry.Operations[entry.Latest]; exists && latest.State == "unknown" {
			return freshstore.ConfigGroupOperation{}, cpa.ErrApplyUnknown
		}
	}
	document, err := auth.wire.ReadConfigDocument(ctx, auth.secret)
	if err != nil {
		return freshstore.ConfigGroupOperation{}, err
	}
	if groupBaseline(document, group) != q.Expected.Baseline {
		return freshstore.ConfigGroupOperation{}, cpa.ErrSourceConflict
	}
	operation := freshstore.ConfigGroupOperation{
		RequestID: q.RequestID, Group: groupID, Target: "group", Prefix: group.Prefix,
		Baseline: q.Expected.Baseline, Edits: edits, State: "unknown",
		SourceID: r.Operation.Identity.InstallationID, Generation: q.Expected.Generation,
	}
	if err = s.appendGroupOperation(ctx, r.Operation.Identity, r.Operation.Revision, groupID, operation); err != nil {
		return operation, err
	}
	// One DELETE per restored leaf, then one PATCH with only the edited leaves.
	for _, edit := range edits {
		if edit.Action != "default" {
			continue
		}
		if err = auth.wire.DeleteConfigPath(ctx, auth.secret, edit.Path); err != nil {
			return s.failGroupOperation(ctx, groupID, operation, err)
		}
	}
	if payload, ok := groupPatch(group.Prefix, edits); ok {
		if err = auth.wire.PatchConfigPath(ctx, auth.secret, group.Prefix, payload); err != nil {
			return s.failGroupOperation(ctx, groupID, operation, err)
		}
	}
	acknowledged := operation
	acknowledged.HTTP200 = true
	if err = s.updateGroupOperation(ctx, groupID, operation, acknowledged); err != nil {
		return operation, err
	}
	return s.resolveGroupOperation(ctx, groupID, auth, acknowledged)
}

// SaveFamily performs exactly one whole-family rule write.
func (s *Service) SaveFamily(ctx context.Context, familyID string, q FamilyRequest) (freshstore.ConfigGroupOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	family, ok := cpa.LookupConfigRuleFamily(familyID)
	if !ok {
		return freshstore.ConfigGroupOperation{}, ErrInvalid
	}
	rules, err := cpa.ParseConfigRules(family.Family, q.Rules)
	if err != nil || validateExpected(q.Expected) != nil || !freshkeysValidRequestID(q.RequestID) {
		return freshstore.ConfigGroupOperation{}, ErrInvalid
	}
	r, err := s.store.Load(ctx)
	if err != nil {
		return freshstore.ConfigGroupOperation{}, err
	}
	auth, err := s.resolve(ctx, r)
	if err != nil || auth.wire == nil {
		return freshstore.ConfigGroupOperation{}, cpa.ErrProofUnavailable
	}
	if !auth.capable.RulesWritable {
		return freshstore.ConfigGroupOperation{}, ErrRulesReadOnly
	}
	if r.ConfigGroupsInvalid {
		return freshstore.ConfigGroupOperation{}, cpa.ErrSourceConflict
	}
	if entry := r.ConfigGroups[cpa.ConfigRulesGroupID]; entry != nil {
		if old, exists := entry.Operations[q.RequestID]; exists {
			if !sameFamilyRequest(old, family.Path, rules, q.Expected.Baseline) {
				return old, cpa.ErrSourceConflict
			}
			return old, nil
		}
		if latest, exists := entry.Operations[entry.Latest]; exists && latest.State == "unknown" {
			return freshstore.ConfigGroupOperation{}, cpa.ErrApplyUnknown
		}
	}
	document, err := auth.wire.ReadConfigDocument(ctx, auth.secret)
	if err != nil {
		return freshstore.ConfigGroupOperation{}, err
	}
	if familyBaseline(document, family.Path) != q.Expected.Baseline {
		return freshstore.ConfigGroupOperation{}, cpa.ErrSourceConflict
	}
	payload, err := json.Marshal(rules)
	if err != nil {
		return freshstore.ConfigGroupOperation{}, ErrInvalid
	}
	operation := freshstore.ConfigGroupOperation{
		RequestID: q.RequestID, Group: cpa.ConfigRulesGroupID, Target: "family", Prefix: family.Path,
		Baseline: q.Expected.Baseline, Rules: rules, State: "unknown",
		SourceID: r.Operation.Identity.InstallationID, Generation: q.Expected.Generation,
	}
	if err = s.appendGroupOperation(ctx, r.Operation.Identity, r.Operation.Revision, cpa.ConfigRulesGroupID, operation); err != nil {
		return operation, err
	}
	if err = auth.wire.PutConfigValue(ctx, auth.secret, family.Path, payload); err != nil {
		return s.failGroupOperation(ctx, cpa.ConfigRulesGroupID, operation, err)
	}
	acknowledged := operation
	acknowledged.HTTP200 = true
	if err = s.updateGroupOperation(ctx, cpa.ConfigRulesGroupID, operation, acknowledged); err != nil {
		return operation, err
	}
	return s.resolveGroupOperation(ctx, cpa.ConfigRulesGroupID, auth, acknowledged)
}

// groupPatch nests the edited leaves below their group prefix. Presence of a key
// is the whole instruction: CPA merges mappings and never invents siblings.
func groupPatch(prefix string, edits []freshstore.ConfigGroupEdit) ([]byte, bool) {
	prefixParts := strings.Split(prefix, ".")
	body := map[string]any{}
	written := false
	for _, edit := range edits {
		if edit.Action != "set" {
			continue
		}
		parts := strings.Split(edit.Path, ".")
		if len(parts) <= len(prefixParts) {
			return nil, false
		}
		cursor := body
		for _, part := range parts[len(prefixParts) : len(parts)-1] {
			next, ok := cursor[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				cursor[part] = next
			}
			cursor = next
		}
		cursor[parts[len(parts)-1]] = edit.Value
		written = true
	}
	if !written {
		return nil, false
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, false
	}
	return payload, true
}

// failGroupOperation records CPA's definite refusal and never repeats the write.
func (s *Service) failGroupOperation(ctx context.Context, group string, operation freshstore.ConfigGroupOperation, cause error) (freshstore.ConfigGroupOperation, error) {
	var rejection cpa.ConfigWriteError
	if !errors.As(cause, &rejection) || !rejection.Definite() {
		// A lost or unreadable response stays unknown until it is reconciled.
		return operation, cpa.ErrApplyUnknown
	}
	next := operation
	next.State, next.Code = "failed", rejection.Code
	if err := s.updateGroupOperation(ctx, group, operation, next); err != nil {
		return operation, err
	}
	return next, Rejection{Code: rejection.Code, Field: rejection.Field, Message: rejection.Message}
}

// reconcileGroup resolves an unknown group operation that was never acknowledged.
func (s *Service) reconcileGroup(ctx context.Context, _ freshstore.Record, auth authority, operation freshstore.ConfigGroupOperation) (freshstore.ConfigGroupOperation, error) {
	return s.resolveGroupOperation(ctx, operation.Group, auth, operation)
}

// resolveGroupOperation records what a read-back proves. Every edited leaf in
// place is the persisted original. A different document is a terminal
// write_not_applied only when CPA never acknowledged the write; after an
// acknowledged write it is the superseded outcome of a later writer.
func (s *Service) resolveGroupOperation(ctx context.Context, group string, auth authority, operation freshstore.ConfigGroupOperation) (freshstore.ConfigGroupOperation, error) {
	if operation.State != "unknown" {
		return operation, nil
	}
	document, err := auth.wire.ReadConfigDocument(ctx, auth.secret)
	if err != nil {
		return operation, cpa.ErrApplyUnknown
	}
	if operationInPlace(document, operation) {
		next := operation
		next.State = "persisted"
		if err = s.updateGroupOperation(ctx, group, operation, next); err != nil {
			return operation, err
		}
		return next, nil
	}
	if operation.HTTP200 {
		next := operation
		next.State, next.Code = "persisted", "superseded"
		if err = s.updateGroupOperation(ctx, group, operation, next); err != nil {
			return operation, err
		}
		return next, nil
	}
	next := operation
	next.State, next.Code = "failed", "write_not_applied"
	if err = s.updateGroupOperation(ctx, group, operation, next); err != nil {
		return operation, err
	}
	return next, nil
}

// operationInPlace reports whether CPA's current document carries exactly the
// value every edit of this operation asked for.
func operationInPlace(document map[string]any, operation freshstore.ConfigGroupOperation) bool {
	switch operation.Target {
	case "family":
		value, presence := cpa.LookupConfigValue(document, operation.Prefix)
		if presence != cpa.ConfigPresent {
			return false
		}
		return canonicalDigest(value) == canonicalDigest(operation.Rules)
	case "group":
		for _, edit := range operation.Edits {
			value, presence := cpa.LookupConfigValue(document, edit.Path)
			switch edit.Action {
			case "default":
				if presence != cpa.ConfigMissing {
					return false
				}
			case "set":
				if presence != cpa.ConfigPresent || canonicalDigest(value) != canonicalDigest(edit.Value) {
					return false
				}
			default:
				return false
			}
		}
		return len(operation.Edits) > 0
	default:
		return false
	}
}

func sameGroupRequest(operation freshstore.ConfigGroupOperation, edits []freshstore.ConfigGroupEdit, baseline string) bool {
	return operation.Target == "group" && operation.Baseline == baseline &&
		canonicalEqual(operation.Edits, edits)
}

func sameFamilyRequest(operation freshstore.ConfigGroupOperation, path string, rules []any, baseline string) bool {
	if operation.Target != "family" || operation.Prefix != path || operation.Baseline != baseline {
		return false
	}
	return canonicalDigest(operation.Rules) == canonicalDigest(rules)
}

func (s *Service) appendGroupOperation(ctx context.Context, identity bootstrap.FreshIdentity, revision uint64, group string, operation freshstore.ConfigGroupOperation) error {
	_, err := s.store.Update(ctx, identity, revision, func(v *freshstore.Record) error {
		if v.ConfigGroups == nil {
			v.ConfigGroups = map[string]*freshstore.ConfigGroupJournal{}
		}
		journal := v.ConfigGroups[group]
		if journal == nil {
			journal = &freshstore.ConfigGroupJournal{Operations: map[string]freshstore.ConfigGroupOperation{}}
			v.ConfigGroups[group] = journal
		}
		if journal.Operations == nil {
			journal.Operations = map[string]freshstore.ConfigGroupOperation{}
		}
		if _, exists := journal.Operations[operation.RequestID]; exists {
			return freshstore.ErrConflict
		}
		if latest, exists := journal.Operations[journal.Latest]; exists && latest.State == "unknown" {
			return freshstore.ErrConflict
		}
		journal.Operations[operation.RequestID] = operation.Clone()
		journal.Latest = operation.RequestID
		return nil
	})
	return err
}

func (s *Service) updateGroupOperation(ctx context.Context, group string, before, next freshstore.ConfigGroupOperation) error {
	r, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, r.Operation.Identity, r.Operation.Revision, func(v *freshstore.Record) error {
		journal := v.ConfigGroups[group]
		if journal == nil || journal.Latest != before.RequestID {
			return freshstore.ErrConflict
		}
		current, ok := journal.Operations[before.RequestID]
		if !ok || !canonicalEqual(current, before) {
			return freshstore.ErrConflict
		}
		journal.Operations[before.RequestID] = next.Clone()
		return nil
	})
	return err
}
