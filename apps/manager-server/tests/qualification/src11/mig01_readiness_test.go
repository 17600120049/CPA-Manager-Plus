// Qualification-only composition of existing product primitives. This file
// is compiled only by go test; it cannot alter product preflight or CLI JSON.
package src11

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyartifact"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

// directCandidateInput belongs to controlled qualification, not an authority
// consumer. The independently retained evidence index supplies both pins. A
// caller's own pins/JSON cannot establish Manager ownership or support policy.
type directCandidateInput struct {
	SourceRoot             string
	ProducerArchive        string
	GenerationRecordSHA256 string
	BundleSHA256           string
}

type directCandidateReadiness struct {
	Scope                  string                      `json:"scope"`
	Status                 string                      `json:"status"`
	SourceRelease          string                      `json:"sourceRelease,omitempty"`
	SourceCommit           string                      `json:"sourceCommit,omitempty"`
	BundleSHA256           string                      `json:"bundleSHA256,omitempty"`
	GenerationRecordSHA256 string                      `json:"generationRecordSHA256,omitempty"`
	Checks                 map[string]string           `json:"checks"`
	Preflight              legacypreflight.Report      `json:"preflight"`
	UpgradeAuthorized      bool                        `json:"UpgradeAuthorized"`
	TrustedHopReceipt      bool                        `json:"TrustedHopReceipt"`
	MigrationReadiness     *requiredMigrationReadiness `json:"migrationReadiness"`
}

type directGenerationRecord struct {
	CaseID                                                                                     string `json:"caseID"`
	SourceRelease, SourceCommit, SourceProducerKind                                            string
	SourceProducerArchiveSHA256, SourceProducerExecutableSHA256, SourceProducerBuildCodeCommit string
	GeneratorCodeCommit, GeneratedBundleSHA256, ControlledInputSHA256                          string
	GenerationEvidenceFiles                                                                    map[string]string
	BundleInventory                                                                            maintenancebundle.Manifest `json:"bundleInventory"`
	Process                                                                                    struct {
		PID      int       `json:"pid"`
		ExitCode int       `json:"exitCode"`
		Reaped   bool      `json:"reaped"`
		Started  time.Time `json:"startedAt"`
		Finished time.Time `json:"finishedAt"`
	} `json:"process"`
	GenerationSuccess                    bool `json:"generationSuccess"`
	UpgradeAuthorized, TrustedHopReceipt bool
}

var errDirectCandidate = errors.New("direct candidate data qualification failed")

// inspectdirectCandidateReadiness executes the maintained production reader,
// classifier, artifact and matching-evidence primitives. It is deliberately
// disconnected from startupAllowed, AdmitStartup, hop validation and all APIs.
// ready means data/compatibility eligibility for the pinned controlled input;
// the embedded diagnostic report's unknown production obligations stay unknown.
// No source migration or repair is permitted. Matching-evidence migration runs
// only on the existing validator's private disposable copies.
func inspectDirectCandidateReadiness(ctx context.Context, in directCandidateInput) (out directCandidateReadiness, err error) {
	out = directCandidateReadiness{Scope: "controlled-direct-candidate-data-compatibility", Status: "blocked", Checks: map[string]string{}}
	if !directDigest(in.GenerationRecordSHA256) || !directDigest(in.BundleSHA256) {
		out.Checks["exact-source-generation"] = "missing-evidence"
		return out, errDirectCandidate
	}
	bundleRoot := filepath.Join(in.SourceRoot, "bundle")
	before, e := maintenancebundle.Verify(ctx, bundleRoot)
	if e != nil || before.SourceRevision != in.BundleSHA256 {
		return out, errDirectCandidate
	}
	defer func() {
		after, e := maintenancebundle.Verify(ctx, bundleRoot)
		if e != nil || !reflect.DeepEqual(before, after) {
			out.Status = "blocked"
			out.Checks["source-unchanged"] = "failed"
			err = errDirectCandidate
		} else {
			out.Checks["source-unchanged"] = "pass"
		}
	}()
	recordBytes, e := directRead(ctx, filepath.Join(in.SourceRoot, "source-generation", "record.json"))
	if e != nil || directSHA(recordBytes) != in.GenerationRecordSHA256 {
		return out, errDirectCandidate
	}
	var record directGenerationRecord
	if json.Unmarshal(recordBytes, &record) != nil || record.CaseID == "" ||
		record.SourceRelease != legacyartifact.Release || record.SourceCommit != legacyartifact.SourceCommit ||
		record.SourceProducerBuildCodeCommit != legacyartifact.SourceCommit || record.SourceProducerKind != "published-artifact" ||
		!record.GenerationSuccess || record.UpgradeAuthorized || record.TrustedHopReceipt ||
		!directCommit(record.GeneratorCodeCommit) || !record.Process.Reaped || record.Process.PID <= 0 ||
		record.Process.ExitCode != 0 || record.Process.Started.IsZero() || !record.Process.Finished.After(record.Process.Started) ||
		record.GeneratedBundleSHA256 != before.SourceRevision || !reflect.DeepEqual(record.BundleInventory, before) {
		return out, errDirectCandidate
	}
	pin, e := legacyartifact.Verify(ctx, in.ProducerArchive, "cpa-manager-plus_v1.14.4_linux_amd64.tar.gz")
	if e != nil || record.SourceProducerArchiveSHA256 != pin.ArchiveSHA256 || record.SourceProducerExecutableSHA256 != pin.ExecutableSHA256 {
		return out, errDirectCandidate
	}
	// Verify every declared generation member with a fixed inventory. Never let
	// source JSON select extra files or escape its evidence root.
	names := []string{"controlled-input.json", "release-info.json", "producer-build.json", "producer.log", "config-write.json", "price-write.json", "quota-write.json", "v1-analytics.json", "v1-export.jsonl", "source-snapshot.json"}
	if len(record.GenerationEvidenceFiles) != len(names) {
		return out, errDirectCandidate
	}
	for _, name := range names {
		b, e := directRead(ctx, filepath.Join(in.SourceRoot, "source-generation", name))
		if e != nil || directSHA(b) != record.GenerationEvidenceFiles[name] {
			return out, errDirectCandidate
		}
		if name == "release-info.json" && directSHA(b) != "b8f7b97cd242c11d95b905e2b402275fc5d664025bc829773a260d1da17d18b5" {
			return out, errDirectCandidate
		}
		if name == "controlled-input.json" && directSHA(b) != record.ControlledInputSHA256 {
			return out, errDirectCandidate
		}
		if name == "producer-build.json" {
			var build debug.BuildInfo
			if json.Unmarshal(b, &build) != nil {
				return out, errDirectCandidate
			}
			var commit, modified string
			for _, setting := range build.Settings {
				if setting.Key == "vcs.revision" {
					commit = setting.Value
				}
				if setting.Key == "vcs.modified" {
					modified = setting.Value
				}
			}
			if commit != legacyartifact.SourceCommit || modified != "false" {
				return out, errDirectCandidate
			}
		}
	}
	// The producer-build observation is checked again by the qualification
	// generator against debug/buildinfo.ReadFile of the actual launched binary.
	// Its digest here binds that observation to the retained execution record.
	out.Checks["exact-source-generation"] = "pass"
	layout := maintenancebundle.BundleLayout(bundleRoot)
	paths := sqlite.InspectionPaths{Database: layout.DatabasePath, Config: layout.ConfigPath, DataKey: layout.DataKeyPath, Archives: layout.ArchivesPath}
	out.Preflight, e = legacypreflight.Inspect(ctx, paths, legacypreflight.Options{SourceStopped: true})
	if e != nil || len(out.Preflight.Blocked) != 0 || out.Preflight.SourceKind != "legacy-manager" || out.Preflight.CompatibilityProfile == nil ||
		out.Preflight.CompatibilityProfile.Status != "recognized" || out.Preflight.CompatibilityProfile.Family != "LegacySchemaC" {
		return out, errDirectCandidate
	}
	out.Checks["legacy-classifier"] = "pass"
	db, e := sql.Open("sqlite", dbURI(layout.DatabasePath, true)+"&immutable=1&_pragma=query_only(1)")
	if e != nil {
		return out, errDirectCandidate
	}
	db.SetMaxOpenConns(1)
	out.MigrationReadiness, e = inspectMigrationReadiness(ctx, db)
	closeErr := db.Close()
	if e != nil || closeErr != nil || !out.MigrationReadiness.completedV1144() {
		return out, errDirectCandidate
	}
	out.Checks["required-migrations"] = "pass"
	if out.Preflight.Observed.History["maintenance"] != "none-observed" || out.Preflight.Observed.History["rawDeleted"] != "none-observed" || out.Preflight.Observed.History["archives"] != "none-pending-observed" || out.Preflight.Observed.HistoryRowsObserved["archives"] != 0 {
		return out, errDirectCandidate
	}
	out.Checks["full-raw-no-active-operation"] = "pass"
	if e := maintenancebundle.ValidateMatchingEvidence(ctx, layout); e != nil {
		return out, errDirectCandidate
	}
	out.Checks["bundle-key-config-sqlite-matching"] = "pass"
	if ctx.Err() != nil {
		return out, errDirectCandidate
	}
	out.Status = "ready"
	out.SourceRelease, out.SourceCommit = record.SourceRelease, record.SourceCommit
	out.BundleSHA256, out.GenerationRecordSHA256 = in.BundleSHA256, in.GenerationRecordSHA256
	return out, nil
}

func directDigest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == sha256.Size && hex.EncodeToString(b) == s
}
func directCommit(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 20 && hex.EncodeToString(b) == s
}
func directSHA(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func directRead(ctx context.Context, path string) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, errDirectCandidate
	}
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return nil, errDirectCandidate
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, errDirectCandidate
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return nil, errDirectCandidate
	}
	b, e := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	if e != nil || len(b) > 16<<20 || ctx.Err() != nil {
		return nil, errDirectCandidate
	}
	return b, nil
}
