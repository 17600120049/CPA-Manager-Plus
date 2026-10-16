package bootstrap

import (
	"errors"
	"fmt"
)

type DiscoveryField string

const (
	DiscoveryMode                   DiscoveryField = "mode"
	DiscoveryAuthenticationRequired DiscoveryField = "authentication_required"
	DiscoveryRecoveryRequired       DiscoveryField = "recovery_required"

	DiscoverySourceCPA       DiscoveryField = "source_cpa"
	DiscoverySourceAdmission DiscoveryField = "source_admission"
	DiscoveryDatabase        DiscoveryField = "database"
	DiscoveryBackup          DiscoveryField = "backup"
	DiscoveryResourceRefs    DiscoveryField = "resource_refs"
	DiscoveryMutations       DiscoveryField = "mutations"
	DiscoveryPreserved       DiscoveryField = "preserved"
	DiscoveryUnsupported     DiscoveryField = "unsupported"
)

func publicDiscoveryField(field DiscoveryField) bool {
	switch field {
	case DiscoveryMode, DiscoveryAuthenticationRequired, DiscoveryRecoveryRequired:
		return true
	default:
		return false
	}
}

func knownDiscoveryField(field DiscoveryField) bool {
	switch field {
	case DiscoveryMode, DiscoveryAuthenticationRequired, DiscoveryRecoveryRequired,
		DiscoverySourceCPA, DiscoverySourceAdmission, DiscoveryDatabase, DiscoveryBackup,
		DiscoveryResourceRefs, DiscoveryMutations, DiscoveryPreserved, DiscoveryUnsupported:
		return true
	default:
		return false
	}
}

func ValidateDiscoveryProjection(authenticated bool, fields []DiscoveryField) error {
	if fields == nil {
		return errors.New("discovery projection must be explicit")
	}
	seen := map[DiscoveryField]bool{}
	for _, field := range fields {
		if !knownDiscoveryField(field) {
			return fmt.Errorf("unknown discovery field %q", field)
		}
		if seen[field] {
			return fmt.Errorf("duplicate discovery field %q", field)
		}
		seen[field] = true
		if !authenticated && !publicDiscoveryField(field) {
			return fmt.Errorf("unauthenticated discovery cannot expose %q", field)
		}
	}
	return nil
}
