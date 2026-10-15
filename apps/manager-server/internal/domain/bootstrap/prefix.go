package bootstrap

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

type PrefixOperationResult string

const (
	PrefixPending  PrefixOperationResult = "pending"
	PrefixApplied  PrefixOperationResult = "applied"
	PrefixUnknown  PrefixOperationResult = "unknown"
	PrefixConflict PrefixOperationResult = "conflict"
)

type PrefixRecoveryAction string

const (
	PrefixInspectFirst    PrefixRecoveryAction = "inspect_first"
	PrefixComplete        PrefixRecoveryAction = "complete"
	PrefixResolveConflict PrefixRecoveryAction = "resolve_conflict"
)

func validControlBasePath(value string) bool {
	if value == "" || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#") || strings.Contains(value, "//") || strings.TrimSpace(value) != value {
		return false
	}
	clean := path.Clean(value)
	return clean == value && value != "." && value != "/"
}

type PrefixOperation struct {
	OperationID              OperationID
	ExpectedRoutingRevision  uint64
	CurrentRoutingRevision   uint64
	DesiredControlBasePath   string
	VerifiedEffectivePath    string
	AuthorityQualified       bool
	CollisionFree            bool
	PublicIdentityHealthy    bool
	StaticAssetsHealthy      bool
	APIAuthenticationHealthy bool
	Result                   PrefixOperationResult
}

func (p PrefixOperation) Validate() error {
	if !validOperationID(p.OperationID) {
		return fmt.Errorf("invalid prefix operation id %q", p.OperationID)
	}
	if !validControlBasePath(p.DesiredControlBasePath) {
		return errors.New("desired controlBasePath is not canonical")
	}
	if p.VerifiedEffectivePath != "" && !validControlBasePath(p.VerifiedEffectivePath) {
		return errors.New("verified effective controlBasePath is not canonical")
	}
	switch p.Result {
	case PrefixPending, PrefixApplied, PrefixUnknown, PrefixConflict:
	default:
		return fmt.Errorf("unknown prefix operation result %q", p.Result)
	}
	return nil
}

func CanCommitPrefix(p PrefixOperation) bool {
	return p.Validate() == nil && p.ExpectedRoutingRevision == p.CurrentRoutingRevision &&
		p.AuthorityQualified && p.CollisionFree && p.PublicIdentityHealthy &&
		p.StaticAssetsHealthy && p.APIAuthenticationHealthy && p.Result == PrefixPending
}

func PrefixActionAfterOperation(p PrefixOperation) (PrefixRecoveryAction, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	switch p.Result {
	case PrefixUnknown, PrefixPending:
		return PrefixInspectFirst, nil
	case PrefixApplied:
		if p.VerifiedEffectivePath != p.DesiredControlBasePath {
			return PrefixResolveConflict, nil
		}
		return PrefixComplete, nil
	case PrefixConflict:
		return PrefixResolveConflict, nil
	default:
		return "", fmt.Errorf("unknown prefix result %q", p.Result)
	}
}
