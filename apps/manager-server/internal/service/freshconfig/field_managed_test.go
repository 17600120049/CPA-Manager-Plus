package freshconfig

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	keyrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeyapply"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshstrategy"
)

// managedFieldRuntime is the installed Runtime identity of a default New
// Managed installation. It is never the private attested identity.
const managedFieldRuntime = "runtime-managed-01"

const managedFieldGeneration = 9

var errNotAConfigWriter = errors.New("runtime mutation is not a config-field writer")

func managedFieldArtifact() string { return "sha256:" + strings.Repeat("b", 64) }

// managedFieldCPA is a destination CPA that requires the installation's
// management key on every management call, reads a missing key as 404 and
// stores exactly the scalars it was told to write.
type managedFieldCPA struct {
	mu         sync.Mutex
	management string
	values     map[string]any
	puts       map[string]int
	gets       map[string]int
	unauthed   int
	putStatus  int
	// rejectStatus answers with a definite refusal and stores nothing.
	rejectStatus int
	// failStatus answers with a 5xx and stores nothing: the write never landed.
	failStatus int
}

func newManagedFieldCPA(management string) *managedFieldCPA {
	return &managedFieldCPA{management: management, values: map[string]any{}, puts: map[string]int{}, gets: map[string]int{}}
}

func (m *managedFieldCPA) serve(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/v8/management/config/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		path := strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, prefix), "/", ".")
		if _, known := cpa.LookupConfigField(path); !known {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		authorized := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") == m.management
		if !authorized {
			m.unauthed++
		}
		m.mu.Unlock()
		if !authorized {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			m.mu.Lock()
			value, present := m.values[path]
			m.gets[path]++
			m.mu.Unlock()
			if !present {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(value)
		case http.MethodPut:
			var raw any
			if json.NewDecoder(r.Body).Decode(&raw) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			value := cpa.NormalizeConfigValue(raw)
			if cpa.ValidateConfigFieldValue(path, value) != nil {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
			m.mu.Lock()
			m.puts[path]++
			status, reject, fail := m.putStatus, m.rejectStatus, m.failStatus
			if reject == 0 && fail == 0 {
				m.values[path] = value
			}
			m.mu.Unlock()
			if reject != 0 {
				w.WriteHeader(reject)
				return
			}
			if fail != 0 {
				w.WriteHeader(fail)
				return
			}
			if status != 0 && status != http.StatusOK {
				// The write landed; only the reply was lost.
				w.WriteHeader(status)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func (m *managedFieldCPA) written(path string) (any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[path]
	return value, ok
}

func (m *managedFieldCPA) writes(path string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.puts[path]
}

func (m *managedFieldCPA) totalWrites() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := 0
	for _, n := range m.puts {
		total += n
	}
	return total
}

func (m *managedFieldCPA) rejected() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.unauthed
}

// managedFieldRuntimeClient reports one ready managed installation.
type managedFieldRuntimeClient struct {
	mu     sync.Mutex
	status model.RuntimeObservedStatus
}

func (r *managedFieldRuntimeClient) Status(context.Context) (model.RuntimeObservedStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status, nil
}

func (r *managedFieldRuntimeClient) ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status.State == model.RuntimeStateReady
}

func (*managedFieldRuntimeClient) Start(context.Context, model.RuntimeMutationRequest) (model.RuntimeOperationResult, error) {
	return model.RuntimeOperationResult{}, errNotAConfigWriter
}

func (*managedFieldRuntimeClient) Stop(context.Context, model.RuntimeMutationRequest) (model.RuntimeOperationResult, error) {
	return model.RuntimeOperationResult{}, errNotAConfigWriter
}

func (*managedFieldRuntimeClient) Restart(context.Context, model.RuntimeMutationRequest) (model.RuntimeOperationResult, error) {
	return model.RuntimeOperationResult{}, errNotAConfigWriter
}

func (*managedFieldRuntimeClient) PrepareUpdate(context.Context, model.RuntimePrepareUpdateRequest) (model.RuntimeOperationResult, error) {
	return model.RuntimeOperationResult{}, errNotAConfigWriter
}

func (*managedFieldRuntimeClient) ActivateUpdate(context.Context, model.RuntimeActivateUpdateRequest) (model.RuntimeOperationResult, error) {
	return model.RuntimeOperationResult{}, errNotAConfigWriter
}

func (*managedFieldRuntimeClient) ObserveUpdateOperation(context.Context, model.RuntimeObserveUpdateOperationRequest) (model.RuntimeUpdateOperationObservation, error) {
	return model.RuntimeUpdateOperationObservation{}, errNotAConfigWriter
}

// managedFieldInstall mirrors apps/manager-server/cmd/cpa-manager-plus/
// fresh_managed.go: the authority is re-read from the committed checkpoint and
// the protected Manager connection on every call.
func managedFieldInstall(st freshstore.Store, protector *security.Protector, endpoint string) freshkeyapply.ManagedSource {
	baseURL, endpointErr := cpa.NormalizeV8BaseURL(endpoint)
	return func(ctx context.Context) (freshkeyapply.ManagedInstall, error) {
		if st == nil || protector == nil || endpointErr != nil {
			return freshkeyapply.ManagedInstall{}, freshkeyapply.ErrUnavailable
		}
		record, err := st.Load(ctx)
		if err != nil {
			return freshkeyapply.ManagedInstall{}, freshkeyapply.ErrUnavailable
		}
		checkpoint := record.Operation.Strategy
		if checkpoint.Strategy != bootstrap.NewManaged || checkpoint.Stage != "committed" ||
			!strings.HasPrefix(checkpoint.DestinationConnectionIdentity, "managed:sha256:") ||
			record.Connection == nil {
			return freshkeyapply.ManagedInstall{}, freshkeyapply.ErrUnavailable
		}
		if cpa.NormalizeBaseURL(record.Connection.CPABaseURL) != baseURL ||
			checkpoint.DestinationConnectionIdentity != freshstrategy.ManagedDestinationIdentity(baseURL) {
			return freshkeyapply.ManagedInstall{}, freshkeyapply.ErrUnavailable
		}
		key, err := protector.UnprotectString(record.Connection.ManagementKey)
		if err != nil || len(key) != 64 {
			return freshkeyapply.ManagedInstall{}, freshkeyapply.ErrUnavailable
		}
		return freshkeyapply.ManagedInstall{
			RunID:         "managed:" + string(record.Operation.Identity.OperationID),
			Generation:    strconv.FormatUint(checkpoint.TargetGeneration, 10),
			Identity:      checkpoint.TargetIdentity,
			ArtifactID:    checkpoint.TargetArtifactID,
			BaseURL:       baseURL,
			ManagementKey: key,
		}, nil
	}
}

// managedFieldFixture builds the real default-install graph: a committed New
// Managed checkpoint plus their protected connection, the install-derived
// authority (freshkeyapply.NewManaged) and a freshconfig service bound to it.
func managedFieldFixture(t *testing.T) (*Service, *managedFieldCPA, freshstore.Store, freshstore.Store, *managedFieldRuntimeClient, string, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "manager.sqlite")
	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	admin, err := security.NewAdminCredential("cpamp_FieldManagedTestAdmin0123456!", "fresh")
	if err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewProtector([]byte("isolated-managed-config-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	// The destination must exist before the checkpoint can name it.
	destination := newManagedFieldCPA(strings.Repeat("e", 64))
	server := destination.serve(t)
	identity := bootstrap.FreshIdentity{AdmissionID: "admission", InstallationID: strings.Repeat("1", 32), Generation: "gen", DatabasePath: dbPath, OperationID: "fresh-operation"}
	st := freshstore.New(db)
	record, err := st.Create(t.Context(), identity, &admin)
	if err != nil {
		t.Fatal(err)
	}
	baseURL, err := cpa.NormalizeV8BaseURL(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := protector.ProtectString(destination.management)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly the checkpoint freshstrategy writes after a committed New Managed
	// installation: managed destination identity, runtime identity, generation
	// and the qualified artifact.
	if _, err = st.Update(t.Context(), identity, record.Operation.Revision, func(v *freshstore.Record) error {
		v.Operation.Strategy = bootstrap.FreshStrategyCheckpoint{
			Strategy:                      bootstrap.NewManaged,
			Stage:                         "committed",
			TargetIdentity:                managedFieldRuntime,
			TargetGeneration:              managedFieldGeneration,
			TargetArtifactID:              managedFieldArtifact(),
			DestinationConnectionIdentity: freshstrategy.ManagedDestinationIdentity(baseURL),
		}
		v.Connection = &model.ManagerCPAConnectionConfig{CPABaseURL: baseURL, ManagementKey: ciphertext}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	keys := freshkeys.New(keyrepo.New(db), protector)
	installed, _, err := keys.Create(t.Context(), strings.Repeat("d", 32))
	if err != nil {
		t.Fatal(err)
	}
	if err = keyrepo.New(db).ClaimInitialInstall(t.Context(), keyrepo.InitialInstall{
		KeyID:         string(installed.Key.ID),
		RunID:         "managed:fresh-operation",
		Generation:    strconv.Itoa(managedFieldGeneration),
		AttemptedAtMS: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	runtime := &managedFieldRuntimeClient{status: model.RuntimeObservedStatus{
		Identity:              model.RuntimeIdentity(managedFieldRuntime),
		Generation:            model.RuntimeGeneration(managedFieldGeneration),
		State:                 model.RuntimeStateReady,
		ActiveGatewayArtifact: &model.ActiveGatewayArtifact{ArtifactID: model.RuntimeArtifactID(managedFieldArtifact()), Version: "v8.0.13"},
	}}
	authority := freshkeyapply.NewManaged(keyrepo.New(db), keys, runtime, baseURL, managedFieldInstall(st, protector, baseURL))
	s := New(st, authority.ConfigTarget)
	s.EnableManaged(baseURL)
	return s, destination, st, freshstore.New(db), runtime, baseURL, db
}

func managedFieldRequest(t *testing.T, s *Service, path string, requestID string, text string) (freshstore.ConfigOperation, error) {
	t.Helper()
	view, err := s.ReadField(t.Context(), path)
	if err != nil {
		return freshstore.ConfigOperation{}, err
	}
	if !view.Editable || view.Expected == nil {
		t.Fatalf("field %s is not editable: %+v", path, view)
	}
	return s.SaveField(t.Context(), path, FieldRequest{RequestID: requestID, Value: []byte(text), Expected: *view.Expected})
}

func fieldJournalOf(t *testing.T, st freshstore.Store, path string) *freshstore.FieldJournal {
	t.Helper()
	record, err := st.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return record.Config[path]
}

// TestManagedConfigFieldWritesEveryAllowlistedField proves the whole default
// installation path for each allowlisted field: authenticated read, one write,
// authenticated read-back and one isolated journal entry.
func TestManagedConfigFieldWritesEveryAllowlistedField(t *testing.T) {
	s, destination, st, _, runtime, _, _ := managedFieldFixture(t)
	if !runtime.ready() {
		t.Fatal("fixture runtime is not ready")
	}
	cases := []struct {
		path  string
		text  string
		value any
	}{
		{"routing.retry.request-retry", "2", 2},
		{"routing.retry.max-retry-interval", "30", 30},
		{"requests.streaming.keepalive-seconds", "15", 15},
		{"requests.streaming.bootstrap-retries", "3", 3},
		{"requests.nonstream-keepalive-interval", "60", 60},
		{"requests.passthrough-headers", "true", true},
	}
	requestID := strings.Repeat("a", 32)
	for i, tc := range cases {
		id := requestID[:31] + string(rune('a'+i))
		operation, err := managedFieldRequest(t, s, tc.path, id, tc.text)
		if err != nil {
			t.Fatalf("%s: write failed: %v", tc.path, err)
		}
		if operation.State != "persisted" || operation.Field != tc.path || operation.RequestID != id {
			t.Fatalf("%s: unexpected operation %+v", tc.path, operation)
		}
		if written, ok := destination.written(tc.path); !ok || written != tc.value {
			t.Fatalf("%s: destination holds %v (present=%v), want %v", tc.path, written, ok, tc.value)
		}
		if n := destination.writes(tc.path); n != 1 {
			t.Fatalf("%s: %d writes, want exactly one", tc.path, n)
		}
		view, err := s.ReadField(t.Context(), tc.path)
		if err != nil {
			t.Fatalf("%s: read-back failed: %v", tc.path, err)
		}
		if view.Presence != "present" || view.Value != tc.value || !view.Editable {
			t.Fatalf("%s: read-back %+v, want present %v", tc.path, view, tc.value)
		}
		journal := fieldJournalOf(t, st, tc.path)
		if journal == nil || len(journal.Operations) != 1 || journal.Latest != id {
			t.Fatalf("%s: journal %+v, want one entry %s", tc.path, journal, id)
		}
		record, err := st.Load(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for path := range record.Config {
			if path != tc.path && len(record.Config[path].Operations) != 1 {
				t.Fatalf("%s: journal of %s was modified", tc.path, path)
			}
		}
		// The management credential never reaches a response, a journal or an error.
		encoded, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		journalled, err := json.Marshal(operation)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), destination.management) || strings.Contains(string(journalled), destination.management) {
			t.Fatalf("%s: management key leaked into the read model or the journal", tc.path)
		}
		if destination.rejected() != 0 {
			t.Fatalf("%s: %d unauthenticated calls, want none", tc.path, destination.rejected())
		}
	}
	if total := destination.totalWrites(); total != len(cases) {
		t.Fatalf("%d writes for %d fields, want one each", total, len(cases))
	}
}

// TestManagedConfigFieldRejectsOutOfRangeBeforePut keeps Manager-side validation
// in front of the only CPA write.
func TestManagedConfigFieldRejectsOutOfRangeBeforePut(t *testing.T) {
	s, destination, st, _, _, _, _ := managedFieldFixture(t)
	for _, tc := range []struct {
		path string
		text string
	}{
		{"routing.retry.request-retry", "11"},
		{"routing.retry.request-retry", "-1"},
		{"routing.retry.max-retry-interval", "301"},
		{"requests.streaming.keepalive-seconds", "-5"},
		{"requests.streaming.bootstrap-retries", "11"},
		{"requests.nonstream-keepalive-interval", "301"},
		{"requests.passthrough-headers", `"yes"`},
		{"requests.passthrough-headers", "2"},
		{"requests.unknown-field", "1"},
	} {
		view, err := s.ReadField(t.Context(), tc.path)
		if err != nil && tc.path != "requests.unknown-field" {
			t.Fatalf("%s: read failed: %v", tc.path, err)
		}
		expected := Expected{SourceID: strings.Repeat("1", 32), Generation: strconv.Itoa(managedFieldGeneration), Revision: 1}
		if view.Expected != nil {
			expected = *view.Expected
		}
		_, err = s.SaveField(t.Context(), tc.path, FieldRequest{RequestID: strings.Repeat("c", 32), Value: []byte(tc.text), Expected: expected})
		if err == nil {
			t.Fatalf("%s=%s was accepted", tc.path, tc.text)
		}
		if tc.path == "requests.unknown-field" {
			continue
		}
		if n := destination.writes(tc.path); n != 0 {
			t.Fatalf("%s=%s reached CPA %d times", tc.path, tc.text, n)
		}
		if journal := fieldJournalOf(t, st, tc.path); journal != nil {
			t.Fatalf("%s=%s was journaled: %+v", tc.path, tc.text, journal)
		}
	}
	if total := destination.totalWrites(); total != 0 {
		t.Fatalf("%d writes before validation, want none", total)
	}
}

// TestManagedConfigFieldUnknownIsReconciledWithoutResend proves a lost reply is
// never replayed: the original request is re-observed and only re-read.
func TestManagedConfigFieldUnknownIsReconciledWithoutResend(t *testing.T) {
	s, destination, st, restarted, _, endpoint, _ := managedFieldFixture(t)
	destination.mu.Lock()
	destination.putStatus = http.StatusServiceUnavailable
	destination.mu.Unlock()
	path := "requests.streaming.keepalive-seconds"
	id := strings.Repeat("b", 32)
	operation, err := managedFieldRequest(t, s, path, id, "20")
	if !errors.Is(err, cpa.ErrApplyUnknown) {
		t.Fatalf("lost reply returned %v, want an unknown result", err)
	}
	if operation.State != "unknown" || operation.RequestID != id {
		t.Fatalf("journal kept %+v, want the original unknown operation", operation)
	}
	if journal := fieldJournalOf(t, st, path); journal == nil || journal.Operations[id].State != "unknown" || journal.Latest != id {
		t.Fatalf("durable journal %+v, want the unknown original latest", journal)
	}
	if n := destination.writes(path); n != 1 {
		t.Fatalf("%d writes after a lost reply, want one", n)
	}
	// A second save is refused: an unknown original is recovered, never retried.
	if _, err = s.SaveField(t.Context(), path, FieldRequest{RequestID: strings.Repeat("9", 32), Value: []byte("21"), Expected: Expected{SourceID: operation.SourceID, Generation: operation.Generation, Revision: operation.Revision, SHA256: operation.SHA256}}); !errors.Is(err, cpa.ErrApplyUnknown) {
		t.Fatalf("second save after an unknown write returned %v", err)
	}
	if n := destination.writes(path); n != 1 {
		t.Fatalf("%d writes after the refused save, want one", n)
	}
	// Recovery runs on a restarted process over the same durable store.
	destination.mu.Lock()
	destination.putStatus = 0
	destination.mu.Unlock()
	restartService := New(restarted, s.target)
	restartService.EnableManaged(endpoint)
	reconciled, err := restartService.OperationField(t.Context(), path, id, true)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if reconciled.State != "persisted" || reconciled.RequestID != id || reconciled.Value != 20 {
		t.Fatalf("reconciled to %+v, want the original request persisted", reconciled)
	}
	if n := destination.writes(path); n != 1 {
		t.Fatalf("%d writes after reconcile, want one", n)
	}
	if hostile, err := restartService.OperationField(t.Context(), path, strings.Repeat("9", 32), true); !errors.Is(err, ErrNotFound) || hostile.RequestID != "" {
		t.Fatalf("unknown request resolved to %+v (%v)", hostile, err)
	}
}

// TestManagedConfigFieldFailsClosedWithoutTheManagementKey keeps both a wrong
// and a missing credential off the write path.
func TestManagedConfigFieldFailsClosedWithoutTheManagementKey(t *testing.T) {
	// A rotated destination credential is a different installation.
	rotated, destination, st, _, _, _, _ := managedFieldFixture(t)
	destination.mu.Lock()
	destination.management = strings.Repeat("f", 64)
	destination.mu.Unlock()
	path := "requests.passthrough-headers"
	// The destination rejects the stale key, so the observation itself fails:
	// there is no source the Manager could claim to have written against.
	view, err := rotated.ReadField(t.Context(), path)
	if !errors.Is(err, cpa.ErrProofUnavailable) || view.Editable {
		t.Fatalf("rotated credential read returned view=%+v err=%v", view, err)
	}
	if _, err = rotated.SaveField(t.Context(), path, FieldRequest{RequestID: strings.Repeat("7", 32), Value: []byte("true"), Expected: Expected{SourceID: strings.Repeat("1", 32), Generation: strconv.Itoa(managedFieldGeneration), Revision: 1}}); err == nil {
		t.Fatal("rotated credential wrote a field")
	}
	if n := destination.totalWrites(); n != 0 {
		t.Fatalf("%d writes with a rotated credential, want none", n)
	}
	if journal := fieldJournalOf(t, st, path); journal != nil {
		t.Fatalf("rotated credential was journaled: %+v", journal)
	}

	// A protector that cannot open the committed connection has no authority at
	// all: the install-derived source fails closed before any field is observed.
	_, other, brokenStore, _, _, brokenBase, brokenDB := managedFieldFixture(t)
	brokenProtector, err := security.NewProtector([]byte("a-different-managed-config-key"))
	if err != nil {
		t.Fatal(err)
	}
	brokenAuthority := freshkeyapply.NewManaged(keyrepo.New(brokenDB), freshkeys.New(keyrepo.New(brokenDB), brokenProtector),
		&managedFieldRuntimeClient{status: model.RuntimeObservedStatus{
			Identity:              model.RuntimeIdentity(managedFieldRuntime),
			Generation:            model.RuntimeGeneration(managedFieldGeneration),
			State:                 model.RuntimeStateReady,
			ActiveGatewayArtifact: &model.ActiveGatewayArtifact{ArtifactID: model.RuntimeArtifactID(managedFieldArtifact()), Version: "v8.0.13"},
		}}, brokenBase, managedFieldInstall(brokenStore, brokenProtector, brokenBase))
	broken := New(brokenStore, brokenAuthority.ConfigTarget)
	broken.EnableManaged(brokenBase)
	view, err = broken.ReadField(t.Context(), path)
	if err != nil {
		t.Fatalf("unreadable credential reported %v", err)
	}
	if view.Editable || view.Reason != "private_source_unavailable" {
		t.Fatalf("unreadable credential is editable: %+v", view)
	}
	if _, err = broken.SaveField(t.Context(), path, FieldRequest{RequestID: strings.Repeat("8", 32), Value: []byte("true"), Expected: Expected{SourceID: strings.Repeat("1", 32), Generation: strconv.Itoa(managedFieldGeneration), Revision: 1}}); err == nil {
		t.Fatal("an unreadable credential wrote a field")
	}
	if n := other.totalWrites(); n != 0 {
		t.Fatalf("%d writes without a credential, want none", n)
	}
	if err != nil && strings.Contains(err.Error(), other.management) {
		t.Fatal("management key leaked into an error")
	}
}

// TestManagedConfigFieldRejectsPrivateShapeOnTheManagedAuthority proves the gate
// accepts exactly the profile this process was built for.
func TestManagedConfigFieldRejectsPrivateShapeOnTheManagedAuthority(t *testing.T) {
	s, destination, st, _, _, _, _ := managedFieldFixture(t)
	path := "requests.streaming.bootstrap-retries"
	record, err := st.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// The private attested shape is not a managed installation.
	if _, err = st.Update(t.Context(), record.Operation.Identity, record.Operation.Revision, func(v *freshstore.Record) error {
		v.Operation.Strategy.TargetIdentity = cpa.PrivateKeyRuntime
		v.Operation.Strategy.TargetArtifactID = cpa.PrivateKeyArtifact
		v.Operation.Strategy.DestinationConnectionIdentity = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := s.ReadField(t.Context(), path)
	if err != nil {
		t.Fatalf("read reported %v", err)
	}
	if view.Editable || view.Reason != "private_source_unavailable" {
		t.Fatalf("private shape accepted by the managed authority: %+v", view)
	}
	if _, err = s.SaveField(t.Context(), path, FieldRequest{RequestID: strings.Repeat("5", 32), Value: []byte("4"), Expected: Expected{SourceID: strings.Repeat("1", 32), Generation: strconv.Itoa(managedFieldGeneration), Revision: 1}}); !errors.Is(err, cpa.ErrProofUnavailable) {
		t.Fatalf("private shape write returned %v", err)
	}
	if n := destination.totalWrites(); n != 0 {
		t.Fatalf("%d writes for a foreign profile, want none", n)
	}
	// A staged (not committed) checkpoint and a committed checkpoint that does
	// not name the managed destination are both outside the gate, and the
	// management authority is never even asked for a credential.
	for name, mutate := range map[string]func(*bootstrap.FreshStrategyCheckpoint){
		"staged": func(c *bootstrap.FreshStrategyCheckpoint) { c.Stage = "staged" },
		"unbound destination": func(c *bootstrap.FreshStrategyCheckpoint) {
			c.DestinationConnectionIdentity = ""
		},
		"private runtime identity": func(c *bootstrap.FreshStrategyCheckpoint) {
			c.TargetIdentity = cpa.PrivateKeyRuntime
			c.TargetArtifactID = cpa.PrivateKeyArtifact
		},
	} {
		bare := bareManagedFieldStore(t, mutate)
		resolved := 0
		service := New(bare, func(context.Context, uint64) (string, error) {
			resolved++
			return strings.Repeat("e", 64), nil
		})
		service.EnableManaged("http://127.0.0.1:18317")
		view, err := service.ReadField(t.Context(), path)
		if err != nil {
			t.Fatalf("%s: read reported %v", name, err)
		}
		if view.Editable {
			t.Fatalf("%s: gate accepted a shape it does not own: %+v", name, view)
		}
		if _, err = service.SaveField(t.Context(), path, FieldRequest{RequestID: strings.Repeat("6", 32), Value: []byte("4"), Expected: Expected{SourceID: strings.Repeat("1", 32), Generation: "9", Revision: 1}}); !errors.Is(err, cpa.ErrProofUnavailable) {
			t.Fatalf("%s: write returned %v", name, err)
		}
		if resolved != 0 {
			t.Fatalf("%s: the management authority was consulted %d times", name, resolved)
		}
	}
}

// bareManagedFieldStore creates a Fresh record whose strategy checkpoint is the
// only thing the gate can read. It has no committed connection of its own.
func bareManagedFieldStore(t *testing.T, mutate func(*bootstrap.FreshStrategyCheckpoint)) freshstore.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "manager.sqlite")
	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	admin, err := security.NewAdminCredential("cpamp_FieldManagedTestAdmin0123456!", "fresh")
	if err != nil {
		t.Fatal(err)
	}
	st := freshstore.New(db)
	identity := bootstrap.FreshIdentity{AdmissionID: "admission", InstallationID: strings.Repeat("1", 32), Generation: "gen", DatabasePath: dbPath, OperationID: "fresh-operation"}
	record, err := st.Create(t.Context(), identity, &admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Update(t.Context(), identity, record.Operation.Revision, func(v *freshstore.Record) error {
		checkpoint := bootstrap.FreshStrategyCheckpoint{
			Strategy:         bootstrap.NewManaged,
			Stage:            "committed",
			TargetIdentity:   managedFieldRuntime,
			TargetGeneration: managedFieldGeneration,
			TargetArtifactID: managedFieldArtifact(),
		}
		if mutate != nil {
			mutate(&checkpoint)
		}
		v.Operation.Strategy = checkpoint
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

// TestManagedConfigFieldRejectionIsTerminal proves a definite upstream refusal:
// CPA rejects the write with a 4xx, nothing lands, the original operation becomes
// a terminal failure with a code, and the field is writable again.
func TestManagedConfigFieldRejectionIsTerminal(t *testing.T) {
	s, destination, st, _, _, _, _ := managedFieldFixture(t)
	path := "requests.streaming.keepalive-seconds"
	destination.mu.Lock()
	destination.rejectStatus = http.StatusUnprocessableEntity
	destination.mu.Unlock()
	id := strings.Repeat("e1", 16)
	operation, err := managedFieldRequest(t, s, path, id, "20")
	if !errors.Is(err, ErrWriteFailed) {
		t.Fatalf("definite rejection returned %v, want a terminal failure", err)
	}
	if operation.State != "failed" || operation.Code != "write_rejected" || operation.RequestID != id {
		t.Fatalf("rejected operation %+v, want failed/write_rejected", operation)
	}
	if n := destination.writes(path); n != 1 {
		t.Fatalf("%d attempts after a definite rejection, want one", n)
	}
	if value, ok := destination.written(path); ok {
		t.Fatalf("a rejected write stored %v", value)
	}
	journal := fieldJournalOf(t, st, path)
	if journal == nil || journal.Operations[id].State != "failed" || journal.Operations[id].Code != "write_rejected" {
		t.Fatalf("journal %+v, want a terminal failed original", journal)
	}
	view, err := s.ReadField(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Editable || view.Reason != "" {
		t.Fatalf("a definite failure locked the field: %+v", view)
	}
	// The failure is terminal evidence, not a lock: a new request ID may write.
	destination.mu.Lock()
	destination.rejectStatus = 0
	destination.mu.Unlock()
	next, err := managedFieldRequest(t, s, path, strings.Repeat("e2", 16), "21")
	if err != nil || next.State != "persisted" {
		t.Fatalf("write after a definite failure: %+v %v", next, err)
	}
	if n := destination.writes(path); n != 2 {
		t.Fatalf("%d attempts after the retry, want two", n)
	}
}

// TestManagedConfigFieldLostRequestResolvesAsNotApplied proves an unknown write
// that never landed is resolved by readback into a terminal failure, without a
// second PUT, and that the field is writable again afterwards.
func TestManagedConfigFieldLostRequestResolvesAsNotApplied(t *testing.T) {
	s, destination, st, restarted, _, endpoint, _ := managedFieldFixture(t)
	path := "requests.nonstream-keepalive-interval"
	destination.mu.Lock()
	destination.failStatus = http.StatusBadGateway
	destination.mu.Unlock()
	id := strings.Repeat("e3", 16)
	operation, err := managedFieldRequest(t, s, path, id, "45")
	if !errors.Is(err, cpa.ErrApplyUnknown) || operation.State != "unknown" {
		t.Fatalf("a write that never landed returned %+v %v", operation, err)
	}
	if value, ok := destination.written(path); ok {
		t.Fatalf("the failed write stored %v", value)
	}
	destination.mu.Lock()
	destination.failStatus = 0
	destination.mu.Unlock()
	// The unknown original is resolved by reading the scalar back, never resent.
	restartService := New(restarted, s.target)
	restartService.EnableManaged(endpoint)
	resolved, err := restartService.OperationField(t.Context(), path, id, true)
	if !errors.Is(err, ErrWriteFailed) {
		t.Fatalf("resolve returned %v, want a terminal failure", err)
	}
	if resolved.State != "failed" || resolved.Code != "write_not_applied" || resolved.RequestID != id {
		t.Fatalf("resolved to %+v, want failed/write_not_applied", resolved)
	}
	if n := destination.writes(path); n != 1 {
		t.Fatalf("%d attempts after the resolve, want one", n)
	}
	journal := fieldJournalOf(t, st, path)
	if journal == nil || journal.Operations[id].State != "failed" || journal.Operations[id].Code != "write_not_applied" {
		t.Fatalf("journal %+v, want a terminal failed original", journal)
	}
	// A resolved failure releases the field for a new, independent write.
	next, err := managedFieldRequest(t, restartService, path, strings.Repeat("e4", 16), "45")
	if err != nil || next.State != "persisted" {
		t.Fatalf("write after a resolved failure: %+v %v", next, err)
	}
	if n := destination.writes(path); n != 2 {
		t.Fatalf("%d attempts after the new write, want two", n)
	}
}
