package src11

import (
	"context"
	"database/sql"
	"errors"
	"reflect"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

var errCacheClearingRecovery = errors.New("cache-accounting-clearing: recovery inspection required; normal startup blocked")

type mig03Readiness struct {
	Scope                 string                      `json:"scope"`
	Status                string                      `json:"status"`
	Blocker               string                      `json:"blocker,omitempty"`
	Recovery              string                      `json:"recovery,omitempty"`
	SourceBundleDigest    string                      `json:"sourceBundleDigest"`
	CandidateBundleDigest string                      `json:"candidateBundleDigest"`
	Migration             *requiredMigrationReadiness `json:"migration"`
	Preflight             legacypreflight.Report      `json:"preflight"`
	UpgradeAuthorized     bool                        `json:"UpgradeAuthorized"`
	TrustedHopReceipt     bool                        `json:"TrustedHopReceipt"`
}

// inspectMIG03Candidate is a qualification-only classifier. It reads the
// persisted production state and never calls discovery, cleanup, a worker, or
// any recovery API. A clearing checkpoint is deliberately not treated as a
// supported resume: the required result is Recovery / block.
func inspectMIG03Candidate(ctx context.Context, bundle string, source maintenancebundle.Manifest) (out mig03Readiness, err error) {
	out = mig03Readiness{
		Scope:              "controlled-direct-candidate-data-compatibility",
		Status:             "blocked",
		SourceBundleDigest: source.SourceRevision,
	}
	before, err := maintenancebundle.Verify(ctx, bundle)
	if err != nil {
		return out, err
	}
	out.CandidateBundleDigest = before.SourceRevision
	defer func() {
		after, verifyErr := maintenancebundle.Verify(ctx, bundle)
		if verifyErr != nil || !reflect.DeepEqual(before, after) {
			out.Status = "blocked"
			err = errDirectCandidate
		}
	}()
	layout := maintenancebundle.BundleLayout(bundle)
	out.Preflight, err = legacypreflight.Inspect(ctx, sqlite.InspectionPaths{
		Database: layout.DatabasePath,
		Config:   layout.ConfigPath,
		DataKey:  layout.DataKeyPath,
		Archives: layout.ArchivesPath,
	}, legacypreflight.Options{SourceStopped: true})
	if err != nil {
		return out, err
	}
	db, err := sql.Open("sqlite", dbURI(layout.DatabasePath, true)+"&immutable=1&_pragma=query_only(1)")
	if err != nil {
		return out, err
	}
	db.SetMaxOpenConns(1)
	out.Migration, err = inspectMigrationReadiness(ctx, db)
	closeErr := db.Close()
	if err != nil || closeErr != nil || out.Migration == nil {
		return out, errors.Join(err, closeErr)
	}
	s := out.Migration
	// The v1.14.4 source is revision 2, but production Discover advances the
	// persisted semantics marker to the revision-3 executor before it can leave
	// a clearing checkpoint. Keep that distinction explicit: source provenance
	// is checked by the caller, while this classifier recognizes the real
	// persisted revision that owns the clearing state.
	if source.SourceRevision == "" || before.SourceRevision == source.SourceRevision ||
		s.CacheRevision == nil || *s.CacheRevision != "3" ||
		len(s.Cache) != 1 || len(s.Quota) != 1 ||
		s.CacheStagingRows == nil || *s.CacheStagingRows != 0 ||
		s.QuotaUnattachedRows == nil || *s.QuotaUnattachedRows != 0 ||
		s.Quota[0].Status != "completed" || s.Quota[0].HasError {
		return out, errDirectCandidate
	}
	c := s.Cache[0]
	if c.Status != "clearing" || c.HasError || c.Changed <= 0 || c.Applied != c.Changed ||
		c.Processed < c.Changed || c.LastID != c.TargetID || c.FinishedAtMS != nil {
		return out, errDirectCandidate
	}
	out.Blocker = "cache-accounting-clearing"
	out.Recovery = "block"
	if out.Preflight.BridgeReadiness == nil || out.Preflight.BridgeReadiness.Status != "blocked" ||
		!containsString(out.Preflight.Blocked, "legacy-bridge-blocked") {
		return out, errCacheClearingRecovery
	}
	return out, errCacheClearingRecovery
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
