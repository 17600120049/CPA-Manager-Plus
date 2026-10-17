package freshstrategy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	runtimeservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/runtime"
)

const realCPA8BinarySHA256 = "b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41"

// These controlled-source runs are implementation evidence, not an independent
// acceptance. Only the test constructor injects resource/profile qualification.
func TestAdoptionExactCPAV8WithSupervisor(t *testing.T) {
	cpaPath, supervisorPath := os.Getenv("CPAMP_TEST_CPA_V8"), os.Getenv("CPAMP_TEST_RUNTIME")
	legacyCPAPath := os.Getenv("CPAMP_TEST_CPA_LEGACY")
	if cpaPath == "" || supervisorPath == "" {
		t.Skip("requires exact CPA candidate and built Supervisor; CI runs explicitly")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("exact evidence requires linux/amd64")
	}
	data, err := os.ReadFile(cpaPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != realCPA8BinarySHA256 {
		t.Fatal("candidate executable digest mismatch")
	}
	for _, class := range []bootstrap.AdoptSourceClass{bootstrap.AdoptNetworkExisting, bootstrap.AdoptLegacyManaged} {
		t.Run(string(class), func(t *testing.T) {
			root := t.TempDir()
			sourceRoot := filepath.Join(root, "source")
			targetRoot := filepath.Join(root, "target")
			for _, dir := range []string{sourceRoot, targetRoot, filepath.Join(sourceRoot, "auth")} {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			adoptionSourceAddr, targetAddr, runtimeAddr := freeTestAddress(t), freeTestAddress(t), freeTestAddress(t)
			sourceKey := "isolated-source-management-key"
			clientKeys := []string{"isolated-client-one", "isolated-client-two"}
			var source *exec.Cmd
			if class == bootstrap.AdoptLegacyManaged {
				if legacyCPAPath == "" {
					t.Fatal("legacy_managed evidence requires pinned historical CPA")
				}
				legacyAddr := freeTestAddress(t)
				_, legacyPort, _ := net.SplitHostPort(legacyAddr)
				legacyConfig := map[string]any{"host": "127.0.0.1", "port": json.Number(legacyPort), "auth-dir": filepath.Join(sourceRoot, "auth"), "remote-management": map[string]any{"secret-key": sourceKey, "disable-control-panel": true}, "api-keys": clientKeys, "plugins": map[string]any{"enabled": false}}
				legacyConfigPath := filepath.Join(sourceRoot, "config.yaml")
				writeTestJSON(t, legacyConfigPath, legacyConfig)
				source = exec.Command(legacyCPAPath, "-config", legacyConfigPath)
				source.Dir = sourceRoot
				if err = source.Start(); err != nil {
					t.Fatal(err)
				}
				defer killTestProcess(source)
				waitClientModels(t, legacyAddr, clientKeys[0])
				beforeLegacyTransition := directoryDigest(t, sourceRoot)

				transitionRoot := filepath.Join(root, "legacy-v8-stage")
				if err = os.Mkdir(transitionRoot, 0o700); err != nil {
					t.Fatal(err)
				}
				if err = os.Mkdir(filepath.Join(transitionRoot, "auth"), 0o700); err != nil {
					t.Fatal(err)
				}
				_, stagePort, _ := net.SplitHostPort(adoptionSourceAddr)
				stageConfig := map[string]any{"host": "127.0.0.1", "port": json.Number(stagePort), "auth-dir": filepath.Join(transitionRoot, "auth"), "remote-management": map[string]any{"secret-key": sourceKey, "disable-control-panel": true}, "api-keys": clientKeys, "plugins": map[string]any{"enabled": false}}
				stageConfigPath := filepath.Join(transitionRoot, "config.yaml")
				writeTestJSON(t, stageConfigPath, stageConfig)
				stagedSource := exec.Command(cpaPath, "-config", stageConfigPath)
				stagedSource.Dir = transitionRoot
				if err = stagedSource.Start(); err != nil {
					t.Fatal(err)
				}
				defer killTestProcess(stagedSource)
				waitTest(t, func() bool {
					_, err := cpa.ReadV8AdoptionKeys(t.Context(), "http://"+adoptionSourceAddr, sourceKey)
					return err == nil
				})
				if beforeLegacyTransition != directoryDigest(t, sourceRoot) {
					t.Fatal("isolated staging-v8 transition changed historical source")
				}
				waitClientModels(t, legacyAddr, clientKeys[0])
			} else {
				_, sourcePort, _ := net.SplitHostPort(adoptionSourceAddr)
				sourceConfig := map[string]any{"config-version": 8, "server": map[string]any{"host": "127.0.0.1", "port": json.Number(sourcePort)}, "oauth": map[string]any{"auth-dir": filepath.Join(sourceRoot, "auth")}, "management": map[string]any{"secret-key": sourceKey, "disable-control-panel": true, "disable-auto-update-panel": true}, "access": map[string]any{"api-keys": clientKeys}, "plugins": map[string]any{"enabled": false}}
				configPath := filepath.Join(sourceRoot, "config.yaml")
				writeTestJSON(t, configPath, sourceConfig)
				source = exec.Command(cpaPath, "-config", configPath)
				source.Dir = sourceRoot
				if err = source.Start(); err != nil {
					t.Fatal(err)
				}
				defer killTestProcess(source)
				waitTest(t, func() bool {
					_, err := cpa.ReadV8AdoptionKeys(t.Context(), "http://"+adoptionSourceAddr, sourceKey)
					return err == nil
				})
			}
			before := directoryDigest(t, sourceRoot)
			manifest := filepath.Join(targetRoot, "artifact.json")
			writeTestJSON(t, manifest, map[string]any{"schemaVersion": 1, "engine": "cpa", "version": "v8.0.13", "artifactId": "sha256:" + realCPA8BinarySHA256})
			token := "isolated-runtime-transport-token"
			supervisor := exec.Command(supervisorPath)
			supervisor.Dir = targetRoot
			supervisor.Env = append(os.Environ(), "CPAMP_RUNTIME_ADDR="+runtimeAddr, "CPAMP_RUNTIME_IDENTITY=isolated-adopt-runtime", "CPAMP_RUNTIME_TOKEN="+token, "CPAMP_RUNTIME_JOURNAL_PATH="+filepath.Join(targetRoot, "operations.sqlite"), "CPAMP_CPA_EXECUTABLE="+cpaPath, "CPAMP_CPA_ARTIFACT_MANIFEST="+manifest, "CPAMP_RUNTIME_CPA_ADDR="+targetAddr)
			if err = supervisor.Start(); err != nil {
				t.Fatal(err)
			}
			defer killTestProcess(supervisor)
			direct := runtimeservice.NewEmbeddedClient("http://"+runtimeAddr, token)
			var status model.RuntimeObservedStatus
			waitTest(t, func() bool { var err error; status, err = direct.Status(t.Context()); return err == nil })
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				current, err := direct.Status(ctx)
				if err == nil {
					_, _ = direct.Stop(ctx, model.RuntimeMutationRequest{OperationID: "test-destination-cleanup", ExpectedRuntimeIdentity: current.Identity, ExpectedRuntimeGeneration: current.Generation})
				}
			}()
			upstream, _ := url.Parse("http://" + runtimeAddr)
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			defer transport.CloseIdleConnections()
			var lostStage, lostStart atomic.Bool
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request := r.Clone(r.Context())
				request.URL.Scheme = upstream.Scheme
				request.URL.Host = upstream.Host
				request.RequestURI = ""
				response, err := transport.RoundTrip(request)
				if err != nil {
					w.WriteHeader(502)
					return
				}
				defer response.Body.Close()
				body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
				lose := r.URL.Path == "/v1/runtime/operations/stage-destination" && !lostStage.Swap(true) || r.URL.Path == "/v1/runtime/operations/start" && !lostStart.Swap(true)
				if lose && response.StatusCode == 200 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = conn.Close()
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(response.StatusCode)
				_, _ = w.Write(body)
			}))
			defer proxy.Close()
			client := runtimeservice.NewEmbeddedClient(proxy.URL, token)
			protector, _ := security.NewProtector([]byte("isolated-exact-cpa-data-key"))
			q := qualified()
			q.StagingV8TransitionQualified = true
			options := Options{Runtime: client, Protector: protector, Qualification: q, QualifiedArtifactID: model.RuntimeArtifactID("sha256:" + realCPA8BinarySHA256), AdoptionResources: acceptedKeys(), DestinationBaseURL: "http://" + targetAddr}
			request := Request{Strategy: bootstrap.AdoptExisting, BaseURL: "http://" + adoptionSourceAddr, ManagementKey: sourceKey, AdoptSourceClass: class, ExpectedRuntimeIdentity: status.Identity, ExpectedRuntimeGeneration: status.Generation, ExpectedArtifactID: options.QualifiedArtifactID}
			dbPath := filepath.Join(root, "manager.sqlite")
			db, err := sqlite.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			admin, _ := security.NewAdminCredential("isolated-cpa-adoption-admin-key", "host")
			identity := bootstrap.FreshIdentity{AdmissionID: "real-admission", InstallationID: "real-installation", Generation: "real-generation", DatabasePath: dbPath, OperationID: "real-adopt-operation"}
			store := freshstore.New(db)
			record, err := store.Create(t.Context(), identity, &admin)
			if err != nil {
				t.Fatal(err)
			}
			staged, err := New(options).Execute(t.Context(), record.Operation, request)
			if err != nil {
				t.Fatal(err)
			}
			record, err = store.Update(t.Context(), identity, record.Operation.Revision, func(r *freshstore.Record) error {
				r.Operation.Strategy = staged.Checkpoint
				r.AdoptionCiphertext = staged.AdoptionCiphertext
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			// Reopen actual SQLite before effects, then reconstruct the adapter for
			// every retry. The frozen encrypted payload survives a Manager restart.
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = sqlite.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			store = freshstore.New(db)
			record, err = store.Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			request.AdoptionCiphertext = record.AdoptionCiphertext
			for i := 0; i < 2; i++ {
				if result, err := New(options).Execute(t.Context(), record.Operation, request); err == nil || result.Checkpoint.Stage == "committed" {
					t.Fatal("lost mutation response committed target")
				}
			}
			var committed Result
			waitTest(t, func() bool {
				var err error
				committed, err = New(options).Execute(t.Context(), record.Operation, request)
				return err == nil && committed.Checkpoint.Stage == "committed"
			})
			record, err = store.Update(t.Context(), identity, record.Operation.Revision, func(r *freshstore.Record) error {
				r.Operation.Strategy = committed.Checkpoint
				r.Connection = &model.ManagerCPAConnectionConfig{CPABaseURL: committed.Connection.BaseURL, ManagementKey: committed.Connection.ManagementKeyCiphertext}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = New(options).Execute(t.Context(), record.Operation, request); err != nil {
				t.Fatal("committed replay", err)
			}
			if before != directoryDigest(t, sourceRoot) {
				t.Fatal("source config or native resource state changed")
			}
			if !lostStage.Load() || !lostStart.Load() || !record.Operation.GatewayPending {
				t.Fatal("lost response/Gateway boundary was not exercised")
			}
			if _, err = direct.Stop(t.Context(), model.RuntimeMutationRequest{OperationID: "test-restart-owned-target", ExpectedRuntimeIdentity: status.Identity, ExpectedRuntimeGeneration: status.Generation}); err != nil {
				t.Fatal(err)
			}
			restarted := exec.Command(supervisorPath)
			restarted.Dir = supervisor.Dir
			restarted.Env = supervisor.Env
			killTestProcess(supervisor)
			if err = restarted.Start(); err != nil {
				t.Fatal(err)
			}
			defer killTestProcess(restarted)
			waitTest(t, func() bool {
				current, err := direct.Status(t.Context())
				return err == nil && current.Generation != status.Generation
			})
			if _, err = New(options).Execute(t.Context(), record.Operation, request); err == nil {
				t.Fatal("new Supervisor incarnation accepted the old Runtime generation")
			}
			if before != directoryDigest(t, sourceRoot) {
				t.Fatal("Supervisor restart changed source")
			}
			keys, err := cpa.ReadV8AdoptionKeys(t.Context(), "http://"+adoptionSourceAddr, sourceKey)
			if err != nil || len(keys) != 2 || keys[0] != clientKeys[0] || keys[1] != clientKeys[1] {
				t.Fatal("qualified staging source no longer usable")
			}
			t.Logf("exact artifact=%s class=%s source-preserved=true isolated-staging-v8=true destination-verified=true sqlite-reopen=true lost-stage-reply=true lost-start-reply=true runtime-restart-fenced=true production-acceptance=pending", options.QualifiedArtifactID, class)
		})
	}
}

func freeTestAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	_ = l.Close()
	return address
}
func writeTestJSON(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
func killTestProcess(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
	}
}
func waitClientModels(t *testing.T, address, key string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	waitTest(t, func() bool {
		request, _ := http.NewRequest(http.MethodGet, "http://"+address+"/v1/models", nil)
		request.Header.Set("Authorization", "Bearer "+key)
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		return response.StatusCode == http.StatusOK
	})
}

func waitTest(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("controlled CPA/Runtime observation did not become available")
}
func directoryDigest(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, _ := filepath.Rel(root, name)
		_, _ = io.WriteString(h, relative)
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		_, _ = h.Write(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
