package bootstrap

import (
	"errors"
	"time"
)

const (
	FreshContractVersion     = 1
	FreshClaimLifetime       = 10 * time.Minute
	FreshSessionLifetime     = 30 * time.Minute
	FixedBootstrapUIPath     = FixedBootstrapDiscoveryPath + "/ui"
	FixedBootstrapAssetsPath = FixedBootstrapDiscoveryPath + "/assets/"
	FixedBootstrapAPIPath    = FixedBootstrapDiscoveryPath + "/api/v2/"
)

// FreshIdentity is non-secret provenance established only by fenced real-store
// admission. Request-supplied identity is never sufficient to create it.
type FreshIdentity struct {
	AdmissionID    string      `json:"admissionId"`
	InstallationID string      `json:"installationId"`
	Generation     string      `json:"generation"`
	DatabasePath   string      `json:"databasePath"`
	OperationID    OperationID `json:"operationId"`
}

func (i FreshIdentity) Valid() bool {
	return i.AdmissionID != "" && i.InstallationID != "" && i.Generation != "" &&
		i.DatabasePath != "" && validOperationID(i.OperationID)
}

type FreshOwnership struct {
	Identity              FreshIdentity `json:"identity"`
	CurrentInstallationID string        `json:"currentInstallationId"`
	CurrentGeneration     string        `json:"currentGeneration"`
	CurrentOperationID    OperationID   `json:"currentOperationId"`
	Revision              uint64        `json:"revision"`
}

func (o FreshOwnership) Valid() bool {
	return o.Identity.Valid() && o.Revision > 0 &&
		o.Identity.InstallationID == o.CurrentInstallationID &&
		o.Identity.Generation == o.CurrentGeneration && o.Identity.OperationID == o.CurrentOperationID
}

// FreshOperation is the product checkpoint, not an authentication record or a
// general workflow engine. It never contains browser/Admin/CPA secrets.
type FreshOperation struct {
	Version          int                     `json:"version"`
	Identity         FreshIdentity           `json:"identity"`
	Revision         uint64                  `json:"revision"`
	AdminInitialized bool                    `json:"adminInitialized"`
	ProductComplete  bool                    `json:"productComplete"`
	GatewayPending   bool                    `json:"gatewayPending"`
	Routing          FreshRoutingCheckpoint  `json:"routing"`
	Strategy         FreshStrategyCheckpoint `json:"strategy"`
}

type FreshRoutingCheckpoint struct {
	OperationID OperationID          `json:"operationId,omitempty"`
	Revision    uint64               `json:"revision"`
	Target      string               `json:"target,omitempty"`
	Stage       PrefixOperationStage `json:"stage,omitempty"`
	Committed   bool                 `json:"committed"`
}

type FreshStrategyCheckpoint struct {
	Strategy         CPAStrategy `json:"strategy,omitempty"`
	TargetIdentity   string      `json:"targetIdentity,omitempty"`
	TargetArtifactID string      `json:"targetArtifactId,omitempty"`
	TargetGeneration uint64      `json:"targetGeneration"`
	Stage            string      `json:"stage,omitempty"`
	Unsupported      []string    `json:"unsupported,omitempty"`
}

func (o FreshOperation) Ownership() FreshOwnership {
	return FreshOwnership{Identity: o.Identity, CurrentInstallationID: o.Identity.InstallationID,
		CurrentGeneration: o.Identity.Generation, CurrentOperationID: o.Identity.OperationID, Revision: o.Revision}
}

func (o FreshOperation) Validate() error {
	if o.Version != FreshContractVersion || !o.Ownership().Valid() || !o.GatewayPending {
		return errors.New("invalid Fresh ownership checkpoint")
	}
	if o.Routing.OperationID == "" {
		if o.Routing.Revision != 0 || o.Routing.Target != "" || o.Routing.Stage != "" || o.Routing.Committed {
			return errors.New("unowned initial routing checkpoint")
		}
	} else {
		if o.Routing.OperationID != o.Identity.OperationID {
			return errors.New("foreign initial routing owner")
		}
		if o.Routing.Revision == 0 || !ValidControlBasePath(o.Routing.Target) {
			return errors.New("invalid initial routing target")
		}
		switch o.Routing.Stage {
		case PrefixIntentPersisted, PrefixRouteActivated, PrefixHealthVerified:
			if o.Routing.Committed {
				return errors.New("inconsistent initial routing commit")
			}
		case PrefixCommitted:
			if !o.Routing.Committed {
				return errors.New("inconsistent initial routing commit")
			}
		default:
			return errors.New("invalid initial routing stage")
		}
	}
	if o.Routing.Committed && (!o.AdminInitialized || o.Routing.Stage != PrefixCommitted || o.Routing.Target == "" || o.Routing.Revision == 0) {
		return errors.New("unverified Fresh route commit")
	}
	if o.ProductComplete && (!o.AdminInitialized || !o.Routing.Committed || o.Strategy.Stage != "committed") {
		return errors.New("Fresh product completion is not established")
	}
	return nil
}
