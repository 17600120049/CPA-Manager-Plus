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

var errCacheApplyingRecovery = errors.New("cache-accounting-applying: supported resume required")

type mig02Readiness struct {
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

// Called only after independent exact-source verification and the recorded
// production migration. Source provenance is revision 2; the transformed
// candidate is explicitly revision 3, never an alleged untouched v1 artifact.
// This test-only diagnostic cannot authorize production startup or Upgrade.
func inspectMIG02Candidate(ctx context.Context, bundle string, source maintenancebundle.Manifest) (out mig02Readiness, err error) {
	out = mig02Readiness{Scope: "controlled-direct-candidate-data-compatibility", Status: "blocked", SourceBundleDigest: source.SourceRevision}
	before, err := maintenancebundle.Verify(ctx, bundle)
	if err != nil {
		return out, err
	}
	out.CandidateBundleDigest = before.SourceRevision
	defer func() {
		after, e := maintenancebundle.Verify(ctx, bundle)
		if e != nil || !reflect.DeepEqual(before, after) {
			out.Status = "blocked"
			err = errDirectCandidate
		}
	}()
	layout := maintenancebundle.BundleLayout(bundle)
	out.Preflight, err = legacypreflight.Inspect(ctx, sqlite.InspectionPaths{Database: layout.DatabasePath, Config: layout.ConfigPath, DataKey: layout.DataKeyPath, Archives: layout.ArchivesPath}, legacypreflight.Options{SourceStopped: true})
	if err != nil || out.Preflight.SourceKind != "legacy-manager" || out.Preflight.CompatibilityProfile == nil || out.Preflight.CompatibilityProfile.Family != "LegacySchemaC" || out.Preflight.CompatibilityProfile.Status != "recognized" {
		return out, errDirectCandidate
	}
	db, e := sql.Open("sqlite", dbURI(layout.DatabasePath, true)+"&immutable=1&_pragma=query_only(1)")
	if e != nil {
		return out, e
	}
	db.SetMaxOpenConns(1)
	out.Migration, e = inspectMigrationReadiness(ctx, db)
	closeErr := db.Close()
	if e != nil || closeErr != nil || out.Migration == nil {
		return out, errDirectCandidate
	}
	s := out.Migration
	if source.SourceRevision == "" || before.SourceRevision == source.SourceRevision || s.CacheRevision == nil || *s.CacheRevision != "3" || len(s.Cache) != 1 || len(s.Quota) != 1 || s.CacheStagingRows == nil || s.QuotaUnattachedRows == nil || *s.QuotaUnattachedRows != 0 || s.Quota[0].Status != "completed" || s.Quota[0].HasError {
		return out, errDirectCandidate
	}
	c := s.Cache[0]
	if c.HasError || c.Processed < c.Changed || c.Changed < 2 || c.LastID != c.TargetID || c.TargetID <= 0 {
		return out, errDirectCandidate
	}
	if c.Status == "applying" && c.Applied > 0 && c.Applied < c.Changed && *s.CacheStagingRows == c.Changed-c.Applied && c.FinishedAtMS == nil {
		out.Blocker = "cache-accounting-applying"
		out.Recovery = "supported-resume"
		return out, errCacheApplyingRecovery
	}
	if c.Status != "completed" || c.Applied != c.Changed || *s.CacheStagingRows != 0 || c.FinishedAtMS == nil {
		return out, errDirectCandidate
	}
	if len(out.Preflight.Blocked) != 0 {
		return out, errDirectCandidate
	}
	if e = maintenancebundle.ValidateMatchingEvidence(ctx, layout); e != nil {
		return out, e
	}
	out.Status = "ready"
	return out, nil
}
