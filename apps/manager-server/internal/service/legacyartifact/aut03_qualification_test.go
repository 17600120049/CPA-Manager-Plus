package legacyartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

const (
	aut03PlanCommit           = "2719d8466951da1ea457bd1a00dca3fa6f09dc43"
	aut03LinuxAsset           = "cpa-manager-plus_v1.14.4_linux_amd64.tar.gz"
	aut03WrongAsset           = "cpa-manager-plus_v1.14.4_darwin_amd64.tar.gz"
	aut03WrongReleaseAsset    = "cpa-manager-plus_v1.14.5_linux_amd64.tar.gz"
	aut03SourceBundleDigest   = "0e106589baef4148ee075ed77ce93bc71e83ca315a377311d62daeb9144236e3"
	aut03BundleManifestSHA256 = "8c9578bbe29911f5339b1c2c7c3b6c773e5ea1b681802af837c5c1c4c74df28c"
	aut03ProducerReleaseID    = 404941931
	aut03ProducerAssetID      = 616064478
)

// Embedded by the committed-source runner, never taken from runtime claims.
var aut03HarnessCommit, aut03TestedTree, aut03HarnessSHA256 string

type aut03Evidence struct {
	SchemaVersion                      int                      `json:"schemaVersion"`
	CaseID                             string                   `json:"caseID"`
	PlanCommit                         string                   `json:"planCommit"`
	QualificationHarnessCommit         string                   `json:"qualificationHarnessCommit"`
	TestedTree                         string                   `json:"testedTree"`
	HarnessSHA256                      string                   `json:"harnessSHA256"`
	RuntimePlatform                    string                   `json:"runtimePlatform"`
	Control                            aut03PreflightControl    `json:"control"`
	TrustedExecutablePreparationPassed bool                     `json:"trustedExecutablePreparationPassed"`
	SourceBeforeSHA256                 string                   `json:"sourceBeforeSHA256"`
	SourceAfterSHA256                  string                   `json:"sourceAfterSHA256"`
	SourceParentUnchanged              bool                     `json:"sourceParentUnchanged"`
	Lane                               string                   `json:"lane"`
	Contract                           aut03Contract            `json:"contract"`
	TrustedInput                       aut03TrustedInput        `json:"trustedInput"`
	Mutations                          []aut03Mutation          `json:"mutations"`
	ExistingCoverage                   []aut03CoverageReference `json:"existingCoverage"`
	RuntimeAssertions                  string                   `json:"runtimeAssertions"`
	CasePass                           bool                     `json:"casePass"`
	IndependentAcceptance              string                   `json:"independentAcceptance"`
	UpgradeAuthorized                  bool                     `json:"UpgradeAuthorized"`
	TrustedHopReceipt                  bool                     `json:"TrustedHopReceipt"`
	Phase4A07Complete                  bool                     `json:"Phase4A07Complete"`
	Phase4AComplete                    bool                     `json:"Phase4AComplete"`
}

type aut03PreflightControl struct {
	InputKind                string          `json:"inputKind"`
	InputManifestDigest      string          `json:"inputManifestDigest"`
	Scope                    string          `json:"scope"`
	ArtifactValidationPassed bool            `json:"artifactValidationPassed"`
	BundleValidationPassed   bool            `json:"bundleValidationPassed"`
	ExpectedError            string          `json:"expectedError"`
	ActualError              string          `json:"actualError"`
	Stage                    string          `json:"stage"`
	ExecutableStarted        bool            `json:"executableStarted"`
	OperationMinted          bool            `json:"operationMinted"`
	OperationPhase           string          `json:"operationPhase"`
	Process                  ProcessEvidence `json:"process"`
	Reaped                   bool            `json:"reaped"`
	OutputCreated            bool            `json:"outputCreated"`
	UpgradeAuthorized        bool            `json:"UpgradeAuthorized"`
	TrustedHopReceipt        bool            `json:"TrustedHopReceipt"`
}

type aut03Contract struct {
	ExactRelease      string `json:"exactRelease"`
	ExactSourceCommit string `json:"exactSourceCommit"`
	ExpectedResult    string `json:"expectedResult"`
	ArtifactScope     string `json:"artifactScope"`
	AuthorityBoundary string `json:"authorityBoundary"`
}

type aut03TrustedInput struct {
	Source               string `json:"source"`
	ImmutableReleaseID   int    `json:"immutableReleaseID"`
	ImmutableAssetID     int    `json:"immutableAssetID"`
	Release              string `json:"release"`
	SourceCommit         string `json:"sourceCommit"`
	Asset                string `json:"asset"`
	ArchiveSHA256        string `json:"archiveSHA256"`
	ArchiveSize          int    `json:"archiveSize"`
	ExecutableSHA256     string `json:"executableSHA256"`
	BundleDigest         string `json:"bundleDigest"`
	BundleSourceRevision string `json:"bundleSourceRevision"`
	BundleManifestSHA256 string `json:"bundleManifestSHA256"`
}

type aut03Mutation struct {
	ID                        string `json:"id"`
	OfflineInputKind          string `json:"offlineInputKind,omitempty"`
	Operation                 string `json:"operation"`
	OriginalArchiveSHA256     string `json:"originalArchiveSHA256"`
	MutatedArtifactSHA256     string `json:"mutatedArtifactSHA256,omitempty"`
	OriginalExecutableSHA256  string `json:"originalExecutableSHA256,omitempty"`
	MutatedExecutableSHA256   string `json:"mutatedExecutableSHA256,omitempty"`
	OriginalBundleDigest      string `json:"originalBundleDigest,omitempty"`
	MutatedBundleDigest       string `json:"mutatedBundleDigest,omitempty"`
	ExpectedRejectionCategory string `json:"expectedRejectionCategory"`
	ExpectedError             string `json:"expectedError"`
	ActualError               string `json:"actualError"`
	ActualRejectionCategory   string `json:"actualRejectionCategory"`
	RejectionStage            string `json:"rejectionStage"`
	MutationOffset            int    `json:"mutationOffset,omitempty"`
	ValidationRejected        bool   `json:"validationRejected"`
	ExecutableStarted         bool   `json:"executableStarted"`
	SourceChanged             bool   `json:"sourceChanged"`
	OutputCreated             bool   `json:"outputCreated"`
	OperationMinted           bool   `json:"operationMinted"`
	ReceiptMinted             bool   `json:"receiptMinted"`
	TrustedEvidenceMinted     bool   `json:"trustedEvidenceMinted"`
	UpgradeAuthorized         bool   `json:"UpgradeAuthorized"`
	TrustedHopReceipt         bool   `json:"TrustedHopReceipt"`
}

type aut03CoverageReference struct {
	Category string `json:"category"`
	Status   string `json:"status"`
	Location string `json:"location"`
	Reason   string `json:"reason"`
}

type aut03EvidenceEnvelope struct {
	Format       string          `json:"format"`
	Record       json.RawMessage `json:"record"`
	RecordSHA256 string          `json:"recordSHA256"`
}

func TestAUT03Qualification(t *testing.T) {
	archivePath := strings.TrimSpace(os.Getenv("CPAMP_AUT03_ARCHIVE"))
	if archivePath == "" {
		t.Skip("requires the immutable sealed qualification archive materialization")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("AUT-03 qualification requires the actual Linux AMD64 integration path")
	}
	if len(aut03HarnessCommit) != 40 || len(aut03TestedTree) != 40 || !offlineDigest(aut03HarnessSHA256) {
		t.Fatal("AUT-03 requires committed-source build provenance; use bin/qualification/run-aut03.py")
	}
	asset := strings.TrimSpace(os.Getenv("CPAMP_AUT03_ASSET"))
	if asset == "" {
		asset = aut03LinuxAsset
	}
	if asset != aut03LinuxAsset {
		t.Fatalf("AUT-03 requires the exact pinned Linux AMD64 candidate, got %q", asset)
	}
	pin, ok := nativePins[asset]
	if !ok {
		t.Fatalf("missing deployment pin for %s", asset)
	}

	ctx := context.Background()
	root := testTempDir(t)
	if err := checkPrivateDirectory(root); err != nil {
		t.Fatalf("AUT-03 private workspace parent: %v", err)
	}
	originalArchive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	originalArchiveSHA256 := aut03Digest(originalArchive)
	if originalArchiveSHA256 != pin.ArchiveSHA256 {
		t.Fatalf("sealed producer archive digest = %s, want %s", originalArchiveSHA256, pin.ArchiveSHA256)
	}
	report, err := Verify(ctx, archivePath, asset)
	if err != nil {
		t.Fatalf("trusted artifact did not verify: %v", err)
	}
	if report.Release != Release || report.SourceCommit != SourceCommit || report.ArchiveSHA256 != pin.ArchiveSHA256 || report.ExecutableSHA256 != pin.ExecutableSHA256 || report.WriterEvidence || report.UpgradeAuthorized {
		t.Fatalf("trusted artifact identity/authority: %+v", report)
	}

	bundlePath := strings.TrimSpace(os.Getenv("CPAMP_AUT03_INPUT_BUNDLE"))
	bundleDigest, bundleSourceRevision, bundleManifestSHA256 := "", "", ""
	sourceBefore := ""
	if bundlePath != "" {
		manifest, verifyErr := maintenancebundle.Verify(ctx, bundlePath)
		if verifyErr != nil {
			t.Fatalf("sealed input bundle did not verify: %v", verifyErr)
		}
		bundleDigest, err = ComputeManifestDigest(manifest)
		if err != nil {
			t.Fatal(err)
		}
		bundleSourceRevision = manifest.SourceRevision
		if bundleSourceRevision != aut03SourceBundleDigest {
			t.Fatalf("unexpected sealed input source revision: %s", bundleSourceRevision)
		}
		bundleManifestSHA256, err = aut03FileDigest(filepath.Join(bundlePath, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		if bundleManifestSHA256 != aut03BundleManifestSHA256 {
			t.Fatalf("sealed input manifest digest = %s, want %s", bundleManifestSHA256, aut03BundleManifestSHA256)
		}
		sourceBefore, err = aut03TreeDigest(bundlePath)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal("AUT-03 runtime requires the sealed input bundle on every platform")
	}
	parentBefore, err := aut03TreeDigest(filepath.Dir(bundlePath))
	if err != nil {
		t.Fatal(err)
	}
	// Only artifact/preconditions are under qualification. Correct bytes must
	// reach real sealed launch and reap, never the earlier ErrOfflineInputs.
	// The subsequent source revalidation rejects cleanup's changed shape; this
	// remains a FAILED diagnostic control, not successful source qualification.
	controlWorkspace := filepath.Join(root, "control")
	control, controlErr := RunOfflineCleanup(ctx, OfflineCleanupOptions{
		ArchivePath: archivePath, Asset: asset, InputBundle: bundlePath,
		ExpectedInputDigest: bundleDigest, Workspace: controlWorkspace,
		Timeout: 2 * time.Minute,
	})
	if !errors.Is(controlErr, ErrInvalidSource) || control != nil {
		t.Fatalf("trusted artifact/precondition control: observation=%+v err=%v", control, controlErr)
	}
	var controlState offlineState
	if _, err := offlineRead(filepath.Join(controlWorkspace, "state.json"), 0o600, &controlState); err != nil {
		t.Fatal(err)
	}
	controlIntent, controlState, err := offlineLoad(controlWorkspace, controlState.OperationID)
	if err != nil || controlState.Phase != "failed" || controlState.Process.Validate() != nil || !controlState.Reaped || controlState.OutputManifestDigest != "" || aut03Exists(filepath.Join(controlWorkspace, "output")) || controlIntent.InputManifestDigest != bundleDigest {
		t.Fatalf("trusted artifact control did not reach launch/reap with honest failed state: state=%+v err=%v", controlState, err)
	}
	evidence := aut03Evidence{
		SchemaVersion:              2,
		CaseID:                     "AUT-03",
		PlanCommit:                 aut03PlanCommit,
		QualificationHarnessCommit: aut03HarnessCommit,
		TestedTree:                 aut03TestedTree,
		HarnessSHA256:              aut03HarnessSHA256,
		RuntimePlatform:            runtime.GOOS + "/" + runtime.GOARCH,
		Control:                    aut03PreflightControl{InputKind: "immutable SRC-11 sealed bundle", InputManifestDigest: bundleDigest, Scope: "artifact/offline preconditions only; FAILED cleanup control, not source qualification", ArtifactValidationPassed: true, BundleValidationPassed: true, ExpectedError: "ErrInvalidSource", ActualError: aut03ErrorName(controlErr), Stage: "post-launch source revalidation after child exit/reap", ExecutableStarted: true, OperationMinted: true, OperationPhase: controlState.Phase, Process: controlState.Process, Reaped: controlState.Reaped},
		SourceBeforeSHA256:         sourceBefore,
		Lane:                       "Lane A",
		Contract: aut03Contract{
			ExactRelease:      Release,
			ExactSourceCommit: SourceCommit,
			ExpectedResult:    "reject / ErrUnverified (upper-level integrity rejection); executable-stage typed rejection is ErrExecutableMismatch; no authority",
			ArtifactScope:     "published v1.14.4 archive and executable identity only",
			AuthorityBoundary: "qualification evidence is diagnostic; no trusted receipt or upgrade grant",
		},
		TrustedInput: aut03TrustedInput{
			Source:               "immutable SRC-11 sealed evidence archive / producer member",
			ImmutableReleaseID:   aut03ProducerReleaseID,
			ImmutableAssetID:     aut03ProducerAssetID,
			Release:              report.Release,
			SourceCommit:         report.SourceCommit,
			Asset:                report.Asset,
			ArchiveSHA256:        report.ArchiveSHA256,
			ArchiveSize:          len(originalArchive),
			ExecutableSHA256:     report.ExecutableSHA256,
			BundleDigest:         bundleDigest,
			BundleSourceRevision: bundleSourceRevision,
			BundleManifestSHA256: bundleManifestSHA256,
		},
		ExistingCoverage: []aut03CoverageReference{
			{Category: "manifest/inventory mismatch; missing/extra/replace required file", Status: "verified-on-trusted-input-only", Location: "bin/qualification/materialize-src11.py", Reason: "Existing materializer validates the complete inventory. Its current adversarial suite does not exercise each inventory mutation; no additional AUT-03 runtime PASS is claimed."},
			{Category: "unsafe path, symlink, hardlink, duplicate", Status: "reused-existing-coverage", Location: "bin/qualification/test_materialize_src11.py and internal/service/legacyartifact/artifact_test.go", Reason: "Existing fail-closed materializer/parser primitives are exercised without copying their implementation."},
			{Category: "target/source metadata mismatch", Status: "covered-by-pinned-identity", Location: "internal/service/legacyartifact/artifact.go", Reason: "Release/source commit and asset are deployment-owned constants; caller claims cannot replace them."},
		},
	}

	mutatedArchive := filepath.Join(root, "altered-archive.tar.gz")
	mutatedArchiveSHA256, archiveOffset, err := aut03MutateByte(originalArchive, mutatedArchive)
	if err != nil {
		t.Fatal(err)
	}
	archiveWorkspace := filepath.Join(root, "archive-rejection-operation")
	archiveTarget := filepath.Join(root, "archive-rejection-executable")
	archiveErr := aut03VerifyAndExtractReject(ctx, mutatedArchive, asset, archiveTarget)
	if !errors.Is(archiveErr, ErrUnverified) {
		t.Fatalf("altered archive accepted: %v", archiveErr)
	}
	if bundlePath != "" && runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		var rejected *OfflineCleanupObservation
		rejected, archiveErr = RunOfflineCleanup(ctx, OfflineCleanupOptions{
			ArchivePath: mutatedArchive, Asset: asset, InputBundle: bundlePath,
			ExpectedInputDigest: bundleDigest, Workspace: archiveWorkspace,
			Timeout: 2 * time.Minute,
		})
		if !errors.Is(archiveErr, ErrUnverified) || rejected != nil {
			t.Fatalf("altered archive crossed offline preflight: %v", archiveErr)
		}
	}
	evidence.Mutations = append(evidence.Mutations, aut03Mutation{
		ID:                        "archive-sha256-mismatch",
		OfflineInputKind:          evidence.Control.InputKind,
		OriginalBundleDigest:      bundleDigest,
		Operation:                 "flip one byte in a copied immutable producer archive before verification",
		OriginalArchiveSHA256:     originalArchiveSHA256,
		MutatedArtifactSHA256:     mutatedArchiveSHA256,
		ExpectedRejectionCategory: "artifact SHA256 mismatch",
		ExpectedError:             "ErrUnverified",
		ActualError:               aut03ErrorName(archiveErr),
		ActualRejectionCategory:   "artifact SHA256 mismatch",
		RejectionStage:            "archive pin before archive inspection/extraction",
		MutationOffset:            archiveOffset,
		ValidationRejected:        true,
		ExecutableStarted:         false,
		SourceChanged:             aut03SourceChanged(bundlePath, sourceBefore),
		OutputCreated:             aut03Exists(archiveTarget) || aut03Exists(filepath.Join(archiveWorkspace, "output")),
		OperationMinted:           aut03Exists(archiveWorkspace),
		ReceiptMinted:             false,
		TrustedEvidenceMinted:     false,
		UpgradeAuthorized:         false,
		TrustedHopReceipt:         false,
	})
	if aut03Exists(archiveTarget) || aut03Exists(archiveWorkspace) {
		t.Fatal("altered archive created an executable or operation workspace")
	}

	wrongAssetTarget := filepath.Join(root, "wrong-asset-executable")
	wrongAssetErr := aut03VerifyAndExtractReject(ctx, archivePath, aut03WrongAsset, wrongAssetTarget)
	if !errors.Is(wrongAssetErr, ErrUnverified) {
		t.Fatalf("archive accepted under a different release asset identity: %v", wrongAssetErr)
	}
	evidence.Mutations = append(evidence.Mutations, aut03Mutation{
		ID:                        "archive-identity-mismatch",
		Operation:                 "reuse trusted bytes with a different pinned platform asset identity",
		OriginalArchiveSHA256:     originalArchiveSHA256,
		MutatedArtifactSHA256:     originalArchiveSHA256,
		ExpectedRejectionCategory: "archive identity mismatch",
		ExpectedError:             "ErrUnverified",
		ActualError:               aut03ErrorName(wrongAssetErr),
		ActualRejectionCategory:   "archive identity mismatch",
		RejectionStage:            "deployment-owned archive pin lookup/digest",
		ValidationRejected:        true,
		ExecutableStarted:         false,
		SourceChanged:             aut03SourceChanged(bundlePath, sourceBefore),
		OutputCreated:             aut03Exists(wrongAssetTarget),
		OperationMinted:           false,
		ReceiptMinted:             false,
		TrustedEvidenceMinted:     false,
		UpgradeAuthorized:         false,
		TrustedHopReceipt:         false,
	})
	if aut03Exists(wrongAssetTarget) {
		t.Fatal("wrong asset identity created an executable")
	}

	_, wrongReleaseErr := Verify(ctx, archivePath, aut03WrongReleaseAsset)
	if !errors.Is(wrongReleaseErr, ErrUnverified) {
		t.Fatalf("archive accepted under an unpinned release identity: %v", wrongReleaseErr)
	}
	evidence.Mutations = append(evidence.Mutations, aut03Mutation{
		ID:                        "release-source-identity-mismatch",
		Operation:                 "claim v1.14.5 for a v1.14.4 archive",
		OriginalArchiveSHA256:     originalArchiveSHA256,
		MutatedArtifactSHA256:     originalArchiveSHA256,
		ExpectedRejectionCategory: "release/source identity mismatch",
		ExpectedError:             "ErrUnverified",
		ActualError:               aut03ErrorName(wrongReleaseErr),
		ActualRejectionCategory:   "release/source identity mismatch",
		RejectionStage:            "deployment-owned release/asset pin lookup",
		ValidationRejected:        true,
		ExecutableStarted:         false,
		SourceChanged:             aut03SourceChanged(bundlePath, sourceBefore),
		OutputCreated:             false,
		OperationMinted:           false,
		ReceiptMinted:             false,
		TrustedEvidenceMinted:     false,
		UpgradeAuthorized:         false,
		TrustedHopReceipt:         false,
	})

	trustedExecutable := filepath.Join(root, "trusted-executable")
	trustedExecutableSHA256, _, err := ExtractExecutable(ctx, archivePath, asset, trustedExecutable)
	if err != nil || trustedExecutableSHA256 != pin.ExecutableSHA256 {
		t.Fatalf("trusted executable extraction: digest=%s err=%v", trustedExecutableSHA256, err)
	}
	trustedSeal, sealErr := prepareOfflineExecutable(ctx, trustedExecutable, pin.ExecutableSHA256)
	if sealErr != nil || trustedSeal == nil {
		t.Fatalf("trusted executable did not pass sealed preparation: %v", sealErr)
	}
	if err := trustedSeal.Close(); err != nil {
		t.Fatal(err)
	}
	evidence.TrustedExecutablePreparationPassed = true
	executableBytes, err := os.ReadFile(trustedExecutable)
	if err != nil {
		t.Fatal(err)
	}
	mutatedExecutable := filepath.Join(root, "altered-executable")
	mutatedExecutableSHA256, executableOffset, err := aut03MutateExecutable(executableBytes, mutatedExecutable)
	if err != nil {
		t.Fatal(err)
	}
	executableErr := offlineExecutable(ctx, mutatedExecutable, pin.ExecutableSHA256)
	if !errors.Is(executableErr, ErrExecutableMismatch) {
		t.Fatalf("altered executable accepted: %v", executableErr)
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		sealed, sealErr := prepareOfflineExecutable(ctx, mutatedExecutable, pin.ExecutableSHA256)
		if !errors.Is(sealErr, ErrExecutableMismatch) || sealed != nil {
			t.Fatalf("altered executable crossed sealed launch preparation: file=%v err=%v", sealed, sealErr)
		}
	}
	evidence.Mutations = append(evidence.Mutations, aut03Mutation{
		ID:                        "executable-sha256-mismatch",
		Operation:                 "flip one byte in the extracted executable before the sealed launch digest check",
		OriginalArchiveSHA256:     originalArchiveSHA256,
		MutatedArtifactSHA256:     originalArchiveSHA256,
		OriginalExecutableSHA256:  pin.ExecutableSHA256,
		MutatedExecutableSHA256:   mutatedExecutableSHA256,
		ExpectedRejectionCategory: "executable SHA256 mismatch",
		ExpectedError:             "ErrExecutableMismatch",
		ActualError:               aut03ErrorName(executableErr),
		ActualRejectionCategory:   "executable SHA256 mismatch",
		RejectionStage:            "extracted executable bytes before process start",
		MutationOffset:            executableOffset,
		ValidationRejected:        true,
		ExecutableStarted:         false,
		SourceChanged:             aut03SourceChanged(bundlePath, sourceBefore),
		OutputCreated:             false,
		OperationMinted:           false,
		ReceiptMinted:             false,
		TrustedEvidenceMinted:     false,
		UpgradeAuthorized:         false,
		TrustedHopReceipt:         false,
	})

	if bundlePath != "" {
		wrongBundleDigest := aut03MutateDigest(bundleDigest)
		bundleWorkspace := filepath.Join(root, "bundle-rejection-operation")
		_, bundleErr := offlineInput(ctx, bundlePath, wrongBundleDigest)
		if !errors.Is(bundleErr, ErrOfflineInputs) {
			t.Fatalf("mismatched input bundle digest accepted: %v", bundleErr)
		}
		if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
			rejected, runtimeErr := RunOfflineCleanup(ctx, OfflineCleanupOptions{
				ArchivePath: archivePath, Asset: asset, InputBundle: bundlePath,
				ExpectedInputDigest: wrongBundleDigest, Workspace: bundleWorkspace,
				Timeout: 2 * time.Minute,
			})
			if !errors.Is(runtimeErr, ErrOfflineInputs) || rejected != nil {
				t.Fatalf("mismatched bundle digest crossed offline preflight: %v", runtimeErr)
			}
		}
		evidence.Mutations = append(evidence.Mutations, aut03Mutation{
			ID:                        "bundle-digest-mismatch-control",
			OfflineInputKind:          "immutable SRC-11 sealed bundle",
			Operation:                 "replace the sealed input manifest digest in the operation request",
			OriginalArchiveSHA256:     originalArchiveSHA256,
			MutatedArtifactSHA256:     originalArchiveSHA256,
			OriginalBundleDigest:      bundleDigest,
			MutatedBundleDigest:       wrongBundleDigest,
			ExpectedRejectionCategory: "bundle digest mismatch",
			ExpectedError:             "ErrOfflineInputs",
			ActualError:               aut03ErrorName(bundleErr),
			ActualRejectionCategory:   "bundle digest mismatch",
			RejectionStage:            "input manifest verification before operation workspace creation",
			ValidationRejected:        true,
			ExecutableStarted:         false,
			SourceChanged:             aut03SourceChanged(bundlePath, sourceBefore),
			OutputCreated:             aut03Exists(filepath.Join(bundleWorkspace, "output")),
			OperationMinted:           aut03Exists(bundleWorkspace),
			ReceiptMinted:             false,
			TrustedEvidenceMinted:     false,
			UpgradeAuthorized:         false,
			TrustedHopReceipt:         false,
		})
		if aut03Exists(bundleWorkspace) {
			t.Fatal("mismatched bundle digest created an operation workspace")
		}
	}

	if bundlePath != "" {
		sourceAfter, digestErr := aut03TreeDigest(bundlePath)
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		if sourceAfter != sourceBefore {
			t.Fatalf("qualification input bundle changed: before=%s after=%s", sourceBefore, sourceAfter)
		}
		evidence.SourceAfterSHA256 = sourceAfter
	}
	parentAfter, err := aut03TreeDigest(filepath.Dir(bundlePath))
	if err != nil || parentAfter != parentBefore {
		t.Fatalf("input parent changed or inspection temp files remain: %v", err)
	}
	evidence.SourceParentUnchanged = true
	// Apart from the isolated failed diagnostic control and deliberate files, no
	// rejection may leave staging, journal, output, moved/restored files or logs.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"control": true, "altered-archive.tar.gz": true, "trusted-executable": true, "altered-executable": true}
	if len(entries) != len(allowed) {
		t.Fatalf("unexpected rejection artifacts: %v", entries)
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			t.Fatalf("unexpected rejection artifact: %s", entry.Name())
		}
	}
	for _, mutation := range evidence.Mutations {
		if !mutation.ValidationRejected || mutation.ExecutableStarted || mutation.SourceChanged || mutation.OutputCreated || mutation.OperationMinted || mutation.ReceiptMinted || mutation.TrustedEvidenceMinted || mutation.UpgradeAuthorized || mutation.TrustedHopReceipt {
			t.Fatalf("fail-closed assertion failed: %+v", mutation)
		}
	}
	evidence.RuntimeAssertions = "pass"
	evidence.CasePass = true
	evidence.IndependentAcceptance = "pending"
	evidence.UpgradeAuthorized = false
	evidence.TrustedHopReceipt = false
	evidence.Phase4A07Complete = false
	evidence.Phase4AComplete = false
	if output := strings.TrimSpace(os.Getenv("CPAMP_AUT03_EVIDENCE")); output != "" {
		aut03WriteEvidence(t, output, evidence)
	}
	t.Logf("AUT-03 runtime pass: archive=%s executable=%s mutations=%d", report.ArchiveSHA256, report.ExecutableSHA256, len(evidence.Mutations))
}

func aut03VerifyAndExtractReject(ctx context.Context, archivePath, asset, target string) error {
	if report, err := Verify(ctx, archivePath, asset); !errors.Is(err, ErrUnverified) || report != (Report{}) {
		return fmt.Errorf("verify accepted report=%+v err=%v", report, err)
	}
	_, _, err := ExtractExecutable(ctx, archivePath, asset, target)
	if err == nil {
		return fmt.Errorf("extract accepted altered artifact")
	}
	return err
}

func aut03MutateByte(original []byte, target string) (string, int, error) {
	if len(original) < 2 {
		return "", 0, errors.New("artifact is too small to mutate")
	}
	mutated := append([]byte(nil), original...)
	offset := len(mutated) / 2
	mutated[offset] ^= 0x01
	if err := os.WriteFile(target, mutated, 0o600); err != nil {
		return "", 0, err
	}
	return aut03Digest(mutated), offset, nil
}

func aut03MutateExecutable(original []byte, target string) (string, int, error) {
	digest, offset, err := aut03MutateByte(original, target)
	if err != nil {
		return "", 0, err
	}
	if err := os.Chmod(target, 0o700); err != nil {
		return "", 0, err
	}
	return digest, offset, nil
}

func aut03MutateDigest(digest string) string {
	if digest == "" {
		return strings.Repeat("0", 64)
	}
	last := digest[len(digest)-1]
	if last == '0' {
		last = '1'
	} else {
		last = '0'
	}
	return digest[:len(digest)-1] + string(last)
}

func aut03Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func aut03FileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return aut03Digest(data), nil
}

func aut03TreeDigest(root string) (string, error) {
	entries := make([]string, 0)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			entries = append(entries, filepath.ToSlash(rel)+"|directory|"+info.Mode().Perm().String()+"\n")
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular input member: %s", path)
		}
		digest, err := aut03FileDigest(path)
		if err != nil {
			return err
		}
		entries = append(entries, filepath.ToSlash(rel)+"|"+digest+"|"+info.Mode().Perm().String()+"\n")
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(entries)
	return aut03Digest([]byte(strings.Join(entries, ""))), nil
}

func aut03SourceChanged(bundlePath, before string) bool {
	if bundlePath == "" || before == "" {
		return false
	}
	after, err := aut03TreeDigest(bundlePath)
	return err != nil || after != before
}

func aut03Exists(path string) bool {
	_, err := os.Lstat(path)
	// Inspection failure cannot certify absence.
	return !errors.Is(err, os.ErrNotExist)
}

func aut03ErrorName(err error) string {
	switch {
	case errors.Is(err, ErrUnverified):
		return "ErrUnverified"
	case errors.Is(err, ErrExecutableMismatch):
		return "ErrExecutableMismatch"
	case errors.Is(err, ErrOfflineInputs):
		return "ErrOfflineInputs"
	case errors.Is(err, ErrInvalidSource):
		return "ErrInvalidSource"
	case err == nil:
		return "<nil>"
	default:
		return err.Error()
	}
}

func aut03WriteEvidence(t *testing.T, path string, record aut03Evidence) {
	t.Helper()
	recordData, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	envelope := aut03EvidenceEnvelope{Format: "aut-03-runtime-v2", Record: recordData, RecordSHA256: aut03Digest(recordData)}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var check aut03EvidenceEnvelope
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(written, &check); err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, check.Record); err != nil {
		t.Fatal(err)
	}
	if check.Format != envelope.Format || check.RecordSHA256 != aut03Digest(compact.Bytes()) {
		t.Fatalf("evidence record metadata does not match actual record bytes")
	}
}
