package lifecycle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/artifact"
	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/cpaprocess"
	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/filetrust"
	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/journal"
)

// DestinationRequest deliberately permits only one native resource: client
// API keys. All paths, listener settings, auth storage and process ownership
// belong to this Supervisor, never to the source or the HTTP caller.
type DestinationRequest struct {
	OperationID               string      `json:"operationId"`
	ExpectedRuntimeIdentity   string      `json:"expectedRuntimeIdentity"`
	ExpectedRuntimeGeneration uint64      `json:"expectedRuntimeGeneration"`
	ExpectedActiveArtifactID  artifact.ID `json:"expectedActiveArtifactId"`
	SourceIdentity            string      `json:"sourceIdentity"`
	SourceClass               string      `json:"sourceClass"`
	APIKeys                   []string    `json:"apiKeys"`
	ManagementKey             string      `json:"managementKey"`
}

// SourceClassNewManaged is the default Fresh New Managed installation. It has
// no external source: its identity is the hashed Fresh operation and it never
// carries client keys. Keys are installed later through the management API.
const SourceClassNewManaged = "new_managed"

// ErrGatewayAuthPresent refuses to replace a bootstrap Gateway whose auth
// directory still holds provider logins the new destination would orphan.
var ErrGatewayAuthPresent = errors.New("bootstrap Gateway auth directory is not empty")

func (r DestinationRequest) Validate() error {
	prefix := "external:sha256:"
	switch r.SourceClass {
	case "network_existing", "legacy_managed":
	case SourceClassNewManaged:
		prefix = "managed:sha256:"
		if len(r.APIKeys) != 0 {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	if validateRequest(r.OperationID, r.ExpectedRuntimeIdentity, r.ExpectedRuntimeGeneration) != nil || !r.ExpectedActiveArtifactID.IsValid() ||
		!strings.HasPrefix(r.SourceIdentity, prefix) || len(r.SourceIdentity) != len(prefix)+64 ||
		len(r.ManagementKey) != 64 || r.APIKeys == nil || len(r.APIKeys) > 128 {
		return ErrInvalidRequest
	}
	if _, err := hex.DecodeString(r.ManagementKey); err != nil {
		return ErrInvalidRequest
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(r.SourceIdentity, prefix)); err != nil {
		return ErrInvalidRequest
	}
	seen := map[string]bool{}
	for _, key := range r.APIKeys {
		if key == "" || key != strings.TrimSpace(key) || len(key) > 4096 || strings.ContainsAny(key, "\x00\r\n") || seen[key] {
			return ErrInvalidRequest
		}
		for _, character := range key {
			if character < 0x21 || character == 0x7f {
				return ErrInvalidRequest
			}
		}
		seen[key] = true
	}
	return nil
}

type destination struct {
	root        string
	port        string
	child       *cpaprocess.ChildIdentity
	gatewayAuth string
}

// EnableDestination uses the existing Supervisor-private root and incarnation.
// The stage snapshot is immutable resource input, not a second operation ledger.
// gatewayAuth is the bootstrap Gateway auth directory a new_managed destination
// replaces; staging refuses to orphan provider logins stored there.
func (e *Executor) EnableDestination(root, gatewayAuth, address string, child *cpaprocess.ChildIdentity) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return ErrInvalidRequest
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	auth, err := filepath.Abs(gatewayAuth)
	if err != nil || gatewayAuth == "" {
		return ErrInvalidRequest
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.destination = &destination{root: abs, port: port, child: child, gatewayAuth: auth}
	_, err = e.destination.load()
	return err
}

// gatewayAuthEmpty reports whether the bootstrap Gateway holds no auth files.
// A missing directory is empty; anything but a real directory is untrusted.
func (d *destination) gatewayAuthEmpty() error {
	info, err := os.Lstat(d.gatewayAuth)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() {
		return ErrPersistenceUnavailable
	}
	entries, err := os.ReadDir(d.gatewayAuth)
	if err != nil {
		return ErrPersistenceUnavailable
	}
	if len(entries) != 0 {
		return ErrGatewayAuthPresent
	}
	return nil
}

func (d *destination) load() (*DestinationRequest, error) {
	info, err := os.Lstat(d.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.IsDir() || filetrust.RequireCurrentProcessOwner(info) != nil || filetrust.RequirePrivateDirectoryPermissions(info) != nil {
		return nil, ErrPersistenceUnavailable
	}
	name := filepath.Join(d.root, "stage.json")
	info, err = os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 || filetrust.RequireCurrentProcessOwner(info) != nil || (os.Geteuid() >= 0 && info.Mode().Perm()&0o077 != 0) {
		return nil, ErrPersistenceUnavailable
	}
	data, err := os.ReadFile(name)
	var r DestinationRequest
	if err != nil || json.Unmarshal(data, &r) != nil || r.Validate() != nil {
		return nil, ErrPersistenceUnavailable
	}
	canonical, _ := json.Marshal(r)
	if !bytes.Equal(data, canonical) {
		return nil, ErrPersistenceUnavailable
	}
	return &r, nil
}

func (e *Executor) StageDestination(ctx context.Context, r DestinationRequest) (journal.Operation, error) {
	if err := r.Validate(); err != nil {
		return journal.Operation{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return journal.Operation{}, err
	}
	if e.closed.Load() || e.destination == nil {
		return journal.Operation{}, ErrPersistenceUnavailable
	}
	// Fingerprint public identity only. Not even a hash of keys enters journal.
	public, _ := json.Marshal([]string{string(r.ExpectedActiveArtifactID), r.SourceIdentity, r.SourceClass})
	intent := journal.Intent{OperationID: r.OperationID, OperationType: "stage_destination", ExpectedRuntimeIdentity: r.ExpectedRuntimeIdentity, ExpectedRuntimeGeneration: r.ExpectedRuntimeGeneration, RequestFingerprint: sha256.Sum256(public)}
	op, found, err := e.journal.Resolve(ctx, e.authority, intent)
	if err != nil {
		return journal.Operation{}, submissionError(err)
	}
	stored, err := e.destination.load()
	if err != nil {
		return journal.Operation{}, err
	}
	// A retried attempt stages the identical destination under a fresh operation
	// ID, because the journal never resumes a terminal operation. Only terminal
	// failure of the superseded attempt makes that safe; the replaced directory
	// is retired, never deleted.
	supersedes := ""
	if stored != nil && !reflect.DeepEqual(*stored, r) {
		if err := e.supersedeDestination(ctx, *stored, r); err != nil {
			return journal.Operation{}, err
		}
		supersedes = stored.OperationID
	}
	if found {
		if op.State == journal.StateSucceeded && stored == nil {
			return journal.Operation{}, ErrPersistenceUnavailable
		}
		return op, nil
	}
	if (stored != nil && supersedes == "") || e.activeSelectionAmbiguous {
		return journal.Operation{}, journal.ErrOperationStateConflict
	}
	if state := e.process.Observe().State; state != cpaprocess.StateNotStarted && state != cpaprocess.StateExited {
		return journal.Operation{}, journal.ErrOperationStateConflict
	}
	if r.SourceClass == SourceClassNewManaged {
		if err := e.destination.gatewayAuthEmpty(); err != nil {
			return journal.Operation{}, err
		}
	}
	if e.artifact == nil {
		return journal.Operation{}, ErrActiveArtifactUnavailable
	}
	if err := refreshExpectedActiveArtifact(e.artifact, e.currentDescriptorLocked(), r.ExpectedActiveArtifactID); err != nil {
		return journal.Operation{}, err
	}
	op, created, err := e.journal.Begin(ctx, e.authority, intent)
	if err != nil {
		e.failRecoveryForAmbiguousBegin(err)
		return journal.Operation{}, submissionError(err)
	}
	if !created {
		return op, nil
	}
	// A new destination supersedes any old child's pending recovery epoch.
	// Reuse the same recovery authority as explicit lifecycle admission.
	e.disableRecovery(RecoveryStateInactive)
	execution := context.Background()
	op, err = e.journal.MarkRunning(execution, e.authority.RuntimeIdentity, r.OperationID)
	if err != nil {
		e.disableRecovery(RecoveryStateManualIntervention)
		return op, ErrPersistenceUnavailable
	}
	if err = e.destination.publish(r, supersedes); err != nil {
		failed, completeErr := e.journal.Complete(execution, e.authority.RuntimeIdentity, r.OperationID, journal.StateFailed, "destination_stage_failed")
		return failed, errors.Join(ErrExecutionFailed, completeErr)
	}
	op, err = e.journal.Complete(execution, e.authority.RuntimeIdentity, r.OperationID, journal.StateSucceeded, "")
	if err != nil {
		e.disableRecovery(RecoveryStateManualIntervention)
	}
	return op, err
}

// supersedeDestination admits a stage that replaces the destination a previous
// attempt published. The journal never resumes a terminal operation, so a
// Manager that observed a terminal failure must retry under a new operation ID;
// refusing that here would make the install uncompletable, while admitting it
// unconditionally would let any caller displace a live destination.
//
// Admission therefore requires the identical destination under a different
// operation ID and terminal failure evidence for the whole superseded attempt:
// a failed stage published nothing usable, and a successful stage whose Start
// is terminally failed never owned a running child. A missing, accepted,
// running or successful Start is an operator decision, not a silent retry.
// The Start operation ID of a destination attempt is that attempt's stage ID
// followed by ":start", the same derivation the Manager submits.
func (e *Executor) supersedeDestination(ctx context.Context, stored, r DestinationRequest) error {
	previous := stored
	previous.OperationID = r.OperationID
	if stored.OperationID == r.OperationID || !reflect.DeepEqual(previous, r) {
		return journal.ErrOperationIDConflict
	}
	stage, err := e.journal.Get(ctx, e.authority.RuntimeIdentity, stored.OperationID)
	if err != nil {
		return ErrPersistenceUnavailable
	}
	if stage.State == journal.StateFailed {
		return nil
	}
	if stage.State != journal.StateSucceeded {
		return journal.ErrOperationStateConflict
	}
	start, err := e.journal.Get(ctx, e.authority.RuntimeIdentity, stored.OperationID+":start")
	if errors.Is(err, journal.ErrOperationNotFound) {
		return journal.ErrOperationStateConflict
	}
	if err != nil {
		return ErrPersistenceUnavailable
	}
	if start.OperationType != "start" || start.State != journal.StateFailed {
		return journal.ErrOperationStateConflict
	}
	return nil
}

// retiredDestination is the durable resting place of a superseded attempt. It
// is content-addressed by the superseded operation ID so a replay cannot
// overwrite the evidence of the attempt it replaced.
func (d *destination) retiredDestination(operationID string) string {
	digest := sha256.Sum256([]byte(operationID))
	return filepath.Join(filepath.Dir(d.root), ".destination-retired-"+hex.EncodeToString(digest[:16]))
}

// publish writes a complete destination beside the live one and swaps it in.
// A superseded attempt's directory is renamed aside, never deleted and never
// left as the live destination, so a failed retry cannot orphan it.
func (d *destination) publish(r DestinationRequest, supersedes string) error {
	parent := filepath.Dir(d.root)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || filetrust.RequireCurrentProcessOwner(info) != nil || filetrust.RequirePrivateDirectoryPermissions(info) != nil {
		return ErrPersistenceUnavailable
	}
	tmp, err := os.MkdirTemp(parent, ".destination-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	auth := filepath.Join(tmp, "auth")
	if err = os.Mkdir(auth, 0o700); err != nil {
		return err
	}
	if d.child != nil {
		if err = os.Chown(tmp, -1, int(d.child.GID)); err != nil {
			return err
		}
		if err = os.Chmod(tmp, 0o710); err != nil {
			return err
		}
		if err = os.Chown(auth, int(d.child.UID), int(d.child.GID)); err != nil {
			return err
		}
	}
	stage, _ := json.Marshal(r)
	if err = writeDestinationFile(filepath.Join(tmp, "stage.json"), stage, 0o600); err != nil {
		return err
	}
	// JSON is a YAML subset accepted by the exact CPA v8 config parser. Only
	// this new target receives resources; the source is never contacted here.
	// Usage statistics are productized for the default Fresh New Managed
	// installation only; Adopt and existing sources keep the base template so an
	// operator's CPA posture is not silently changed.
	usage := r.SourceClass == SourceClassNewManaged
	config := map[string]any{"config-version": 8, "server": map[string]any{"host": "0.0.0.0", "port": json.Number(d.port)},
		"management": map[string]any{"allow-remote": true, "secret-key": r.ManagementKey, "disable-control-panel": true, "disable-auto-update-panel": true},
		"access":     map[string]any{"api-keys": r.APIKeys}, "oauth": map[string]any{"auth-dir": filepath.Join(d.root, "auth")},
		"plugins": map[string]any{"enabled": false}, "observability": map[string]any{"logs": map[string]any{"logging-to-file": false}, "usage": map[string]any{"usage-statistics-enabled": usage}}}
	if d.child == nil {
		config["server"].(map[string]any)["host"] = "127.0.0.1"
	}
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	configPath := filepath.Join(tmp, "config.yaml")
	if err = writeDestinationFile(configPath, data, 0o600); err != nil {
		return err
	}
	// CPA hashes its own management key at first startup; the immutable stage
	// snapshot remains Supervisor-only, while this config is child-owned.
	if d.child != nil {
		if err = os.Chown(configPath, int(d.child.UID), int(d.child.GID)); err != nil {
			return err
		}
		f, err := os.Open(configPath)
		if err != nil {
			return err
		}
		if err = errors.Join(f.Sync(), f.Close()); err != nil {
			return err
		}
	}
	if err = syncDestinationDirectory(auth); err != nil {
		return err
	}
	if err = syncDestinationDirectory(tmp); err != nil {
		return err
	}
	retired := ""
	if supersedes != "" {
		retired = d.retiredDestination(supersedes)
		if _, err = os.Lstat(retired); !errors.Is(err, os.ErrNotExist) {
			return ErrPersistenceUnavailable
		}
		if err = os.Rename(d.root, retired); err != nil {
			return err
		}
	}
	if err = os.Rename(tmp, d.root); err != nil {
		if retired == "" {
			return err
		}
		// A failed retry must not leave the Supervisor without a destination;
		// restore the superseded one before reporting the failure.
		return errors.Join(err, os.Rename(retired, d.root))
	}
	return syncDestinationDirectory(parent)
}

func writeDestinationFile(name string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

func syncDestinationDirectory(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func (e *Executor) startProcess(ctx context.Context, executable string) (cpaprocess.Observation, error) {
	spec := cpaprocess.StartSpec{Executable: executable}
	if e.destination != nil {
		staged, err := e.destination.load()
		if err != nil {
			return cpaprocess.Observation{}, err
		}
		if staged != nil {
			// Every Start, Restart and recovery spawn preserves this destination
			// and exact artifact, including after Supervisor reconstruction.
			f, err := os.Open(executable)
			if err != nil {
				return cpaprocess.Observation{}, err
			}
			h := sha256.New()
			_, hashErr := io.Copy(h, f)
			closeErr := f.Close()
			if hashErr != nil || closeErr != nil || "sha256:"+hex.EncodeToString(h.Sum(nil)) != string(staged.ExpectedActiveArtifactID) {
				return cpaprocess.Observation{}, ErrActiveArtifactMismatch
			}
			spec.Args = []string{"-config", filepath.Join(e.destination.root, "config.yaml")}
		}
	}
	return e.process.Start(ctx, spec)
}
