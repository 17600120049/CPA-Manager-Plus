package freshstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

func TestAdoptionSnapshotAndDestinationCommitShareExistingFreshCAS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.sqlite")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	id := bootstrap.FreshIdentity{AdmissionID: "a", InstallationID: "i", Generation: "g", DatabasePath: path, OperationID: "adopt-operation"}
	admin, _ := security.NewAdminCredential("isolated-fresh-adopt-admin-key", "host")
	store := New(db)
	record, err := store.Create(t.Context(), id, &admin)
	if err != nil {
		t.Fatal(err)
	}
	protector, _ := security.NewProtector([]byte("isolated-adopt-store-key"))
	snapshot, _ := protector.ProtectString("test-only encrypted native snapshot")
	baseURL := "http://destination:8317"
	digest := sha256.Sum256([]byte(baseURL))
	checkpoint := bootstrap.FreshStrategyCheckpoint{Strategy: bootstrap.AdoptExisting, Stage: "staged", TargetIdentity: "owned-runtime", TargetGeneration: 4, TargetArtifactID: "sha256:" + strings.Repeat("a", 64), SourceIdentity: "external:sha256:" + strings.Repeat("b", 64), SourceClass: bootstrap.AdoptNetworkExisting, DestinationConnectionIdentity: "external:sha256:" + hex.EncodeToString(digest[:]), AdoptionDecisions: []bootstrap.AdoptionResourceDecision{{ResourceRef: "client-api-keys", Kind: bootstrap.APIKeys, Scope: bootstrap.ClientAPIKeys, ReadEvidenceRef: "preflight:keys-read", ReplayEvidenceRef: "preflight:keys-replay", VerifyEvidenceRef: "preflight:keys-verify", Qualification: bootstrap.CapabilityQualified, Disposition: bootstrap.Migrate}}}
	if _, err = store.Update(t.Context(), id, record.Operation.Revision, func(r *Record) error { r.Operation.Strategy = checkpoint; return nil }); !errors.Is(err, ErrNotFresh) {
		t.Fatal("stage without protected snapshot accepted", err)
	}
	record, err = store.Update(t.Context(), id, record.Operation.Revision, func(r *Record) error { r.Operation.Strategy = checkpoint; r.AdoptionCiphertext = snapshot; return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Record){func(r *Record) { r.AdoptionCiphertext = "plaintext" }, func(r *Record) { r.Operation.Strategy.TargetGeneration++ }, func(r *Record) { r.Operation.Strategy.SourceClass = bootstrap.AdoptLegacyManaged }, func(r *Record) { r.Operation.Strategy.AdoptionDecisions[0].ReadEvidenceRef = "preflight:other-read" }} {
		if _, err = store.Update(t.Context(), id, record.Operation.Revision, func(r *Record) error { mutate(r); return nil }); !errors.Is(err, ErrConflict) {
			t.Fatal("frozen adoption intent changed", err)
		}
	}
	for _, base := range []string{"", "http://foreign:8317"} {
		if _, err = store.Update(t.Context(), id, record.Operation.Revision, func(r *Record) error {
			r.Operation.Strategy.Stage = "committed"
			if base != "" {
				r.Connection = &model.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: snapshot}
			}
			return nil
		}); !errors.Is(err, ErrNotFresh) {
			t.Fatal("unmatched destination committed", err)
		}
	}
	record, err = store.Update(t.Context(), id, record.Operation.Revision, func(r *Record) error {
		r.Operation.Strategy.Stage = "committed"
		r.Connection = &model.ManagerCPAConnectionConfig{CPABaseURL: baseURL, ManagementKey: snapshot}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := New(db).Load(t.Context())
	if err != nil || reopened.AdoptionCiphertext != snapshot || reopened.Operation.Strategy.Stage != "committed" || reopened.Connection == nil || reopened.Connection.CPABaseURL != baseURL {
		t.Fatal("atomic adoption state lost on reopen", err)
	}
}
