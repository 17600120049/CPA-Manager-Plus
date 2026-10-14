package legacyartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyprofile"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

var (
	ErrOfflinePlatform = errors.New("offline cleanup execution is qualified only on Linux AMD64 and Darwin AMD64")
	ErrOfflineBusy     = errors.New("offline operation workspace is still owned by a process")
	ErrOfflineState    = errors.New("offline operation state is missing, invalid, changed or incomplete")
	ErrOfflineReuse    = errors.New("offline workspace already exists; retry requires a new workspace")
	ErrOfflineInputs   = errors.New("offline operation inputs are invalid or changed")
)

const offlineTool = "manager-offline-cleanup/v1"

// OfflineCleanupOptions authorizes one fixed command on a private copy of a
// complete sealed bundle. No runner, executable, authority or target override is
// accepted. ExpectedInputDigest binds the operator's selection before staging.
type OfflineCleanupOptions struct {
	ArchivePath         string
	Asset               string
	InputBundle         string
	ExpectedInputDigest string
	Workspace           string
	Timeout             time.Duration
}

type offlineIntent struct {
	Tool                string `json:"tool"`
	OperationID         string `json:"operationId"`
	Release             string `json:"release"`
	SourceCommit        string `json:"sourceCommit"`
	Asset               string `json:"asset"`
	ArchiveSHA256       string `json:"archiveSHA256"`
	ExecutableSHA256    string `json:"executableSHA256"`
	InputBundle         string `json:"inputBundle"`
	InputManifestDigest string `json:"inputManifestDigest"`
	TimeoutMS           int64  `json:"timeoutMs"`
}

type offlineState struct {
	OperationID          string             `json:"operationId"`
	IntentDigest         string             `json:"intentDigest"`
	Phase                string             `json:"phase"`
	Process              ProcessEvidence    `json:"process"`
	Reaped               bool               `json:"reaped"`
	OutputManifestDigest string             `json:"outputManifestDigest,omitempty"`
	Checkpoints          CheckpointEvidence `json:"checkpoints"`
}

// OfflineCleanupObservation is diagnostic data, not a receipt or authority.
// Even an intact on-disk operation cannot establish a bootstrapped Manager
// owner. InspectOfflineCleanup never returns ValidatedHopEvidence.
type OfflineCleanupObservation struct {
	Status               string             `json:"status"`
	Tool                 string             `json:"tool"`
	OperationID          string             `json:"operationId"`
	Release              string             `json:"release"`
	SourceCommit         string             `json:"sourceCommit"`
	Asset                string             `json:"asset"`
	ArchiveSHA256        string             `json:"archiveSHA256"`
	ExecutableSHA256     string             `json:"executableSHA256"`
	InputManifestDigest  string             `json:"inputManifestDigest"`
	OutputManifestDigest string             `json:"outputManifestDigest"`
	Process              ProcessEvidence    `json:"process"`
	Reaped               bool               `json:"reaped"`
	Checkpoints          CheckpointEvidence `json:"checkpoints"`
	WriterEvidence       bool               `json:"writerEvidence"`
	UpgradeAuthorized    bool               `json:"upgradeAuthorized"`
}

func offlineSupported() bool {
	return runtime.GOARCH == "amd64" && (runtime.GOOS == "linux" || runtime.GOOS == "darwin")
}

// RunOfflineCleanup executes the authenticated published early-dispatch
// cleanup-derived command, never the legacy server. It cannot migrate SchemaA/B
// or complete cache accounting. All failures return no success observation.
func RunOfflineCleanup(ctx context.Context, opts OfflineCleanupOptions) (result *OfflineCleanupObservation, err error) {
	if !offlineSupported() {
		return nil, ErrOfflinePlatform
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if opts.Timeout <= 0 || opts.Timeout > 10*time.Minute || opts.Timeout.Milliseconds() == 0 ||
		opts.Asset != "cpa-manager-plus_v1.14.4_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz" ||
		!offlineDigest(opts.ExpectedInputDigest) {
		return nil, ErrOfflineInputs
	}
	opts.InputBundle, err = offlinePath(opts.InputBundle)
	if err != nil {
		return nil, ErrOfflineInputs
	}
	opts.Workspace, err = offlinePath(opts.Workspace)
	if err != nil || offlineOverlap(opts.Workspace, opts.InputBundle) {
		return nil, ErrOfflineInputs
	}
	if _, e := os.Lstat(opts.Workspace); e == nil {
		return nil, ErrOfflineReuse
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, ErrOfflineInputs
	}
	if e := checkPrivateDirectory(filepath.Dir(opts.Workspace)); e != nil {
		return nil, ErrOfflineInputs
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	artifact, err := Verify(ctx, opts.ArchivePath, opts.Asset)
	if err != nil {
		return nil, err
	}
	input, err := offlineInput(ctx, opts.InputBundle, opts.ExpectedInputDigest)
	if err != nil {
		return nil, err
	}
	if err := offlineSource(ctx, maintenancebundle.BundleLayout(opts.InputBundle)); err != nil {
		return nil, err
	}
	id, err := GenerateOperationID()
	if err != nil {
		return nil, ErrOfflineState
	}
	intent := offlineIntent{offlineTool, id, Release, SourceCommit, opts.Asset, artifact.ArchiveSHA256, artifact.ExecutableSHA256, opts.InputBundle, opts.ExpectedInputDigest, opts.Timeout.Milliseconds()}
	intentData, err := offlineJSON(intent)
	if err != nil {
		return nil, ErrOfflineState
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.Mkdir(opts.Workspace, 0o700); err != nil {
		return nil, ErrOfflineReuse
	}
	lock, err := acquireOfflineLock(filepath.Join(opts.Workspace, ".operation.lock"))
	if err != nil {
		return nil, err
	}
	defer lock.close()
	state := offlineState{OperationID: id, IntentDigest: offlineHash(intentData), Phase: "prepared"}
	// Persist immutable intent and initial state before extraction, staging or
	// launch. Interrupted workspaces are never silently resumed or overwritten.
	if err := offlineWriteNew(filepath.Join(opts.Workspace, "intent.json"), intentData, 0o400); err != nil {
		return nil, ErrOfflineState
	}
	if err := offlineSave(opts.Workspace, state); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			state.Phase = "failed"
			state.OutputManifestDigest = ""
			err = errors.Join(err, offlineSave(opts.Workspace, state))
		}
	}()
	for _, name := range []string{"bin", "tmp"} {
		if err := os.Mkdir(filepath.Join(opts.Workspace, name), 0o700); err != nil {
			return nil, ErrOfflineState
		}
	}
	exe := filepath.Join(opts.Workspace, "bin", "cpa-manager-plus")
	if _, _, err := ExtractExecutable(ctx, opts.ArchivePath, opts.Asset, exe); err != nil {
		return nil, err
	}
	working, err := maintenancebundle.RestoreNew(ctx, opts.InputBundle, filepath.Join(opts.Workspace, "working"))
	if err != nil {
		return nil, ErrOfflineInputs
	}
	if err := maintenancebundle.Revalidate(ctx, working, input); err != nil {
		return nil, ErrOfflineInputs
	}
	if _, err := offlineInput(ctx, opts.InputBundle, opts.ExpectedInputDigest); err != nil {
		return nil, err
	}
	if err := offlineSource(ctx, working); err != nil {
		return nil, err
	}
	if err := offlineExecutable(ctx, exe, intent.ExecutableSHA256); err != nil {
		return nil, err
	}
	// Recheck immutable authorization after staging; ordinary artifact JSON is
	// never used to authorize execution.
	if err := offlineCheckIntent(opts.Workspace, intentData, state); err != nil {
		return nil, err
	}
	if err := offlineRunProcess(ctx, exe, opts.Workspace, working.DatabasePath, lock, &state); err != nil {
		return nil, err
	}
	if err := offlineCheckIntent(opts.Workspace, intentData, state); err != nil {
		return nil, err
	}
	if _, err := offlineInput(ctx, opts.InputBundle, opts.ExpectedInputDigest); err != nil {
		return nil, err
	}
	if err := offlineExecutable(ctx, exe, intent.ExecutableSHA256); err != nil {
		return nil, err
	}
	if err := offlineSource(ctx, working); err != nil {
		return nil, err
	}
	outputLock, err := processlock.Acquire(working.DatabasePath)
	if err != nil {
		return nil, ErrOfflineBusy
	}
	defer outputLock.Close()
	state.Checkpoints, err = inspectCheckpoints(ctx, working.DatabasePath, outputLock)
	if err != nil {
		return nil, err
	}
	if err := offlineSave(opts.Workspace, state); err != nil {
		return nil, err
	}
	if err := offlineCheckIntent(opts.Workspace, intentData, state); err != nil {
		return nil, err
	}
	output, err := maintenancebundle.Create(ctx, working, filepath.Join(opts.Workspace, "output"), outputLock)
	if err != nil {
		return nil, ErrOfflineInputs
	}
	state.OutputManifestDigest, err = ComputeManifestDigest(output)
	if err != nil {
		return nil, err
	}
	if _, err := offlineInput(ctx, opts.InputBundle, opts.ExpectedInputDigest); err != nil {
		return nil, err
	}
	if err := offlineSave(opts.Workspace, state); err != nil {
		return nil, err
	}
	if err := offlineCheckIntent(opts.Workspace, intentData, state); err != nil {
		return nil, err
	}
	state.Phase = "sealed"
	if err := offlineSave(opts.Workspace, state); err != nil {
		return nil, err
	}
	return offlineInspectChecked(ctx, opts.Workspace, id)
}

// Private lifecycle primitive: the exported producer authenticates its own
// executable before reaching this function. Tests of arbitrary processes here
// establish lifecycle behavior only, never published writer qualification.
func offlineRunProcess(ctx context.Context, exe, root, dbPath string, lock *offlineLock, state *offlineState) error {
	cmd := exec.CommandContext(ctx, exe, "cleanup-derived", "--db-path", dbPath)
	cmd.Dir = root
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "TMPDIR=" + filepath.Join(root, "tmp")}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	configureOfflineProcess(cmd, lock)
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return ErrProcessExecutionFailed
	}
	state.Phase, state.Process.PID, state.Process.StartedAtMS = "running", cmd.Process.Pid, started.UnixMilli()
	// Once Start succeeds, every path calls Wait exactly once.
	saveErr := offlineSave(root, *state)
	if saveErr != nil {
		_ = cmd.Cancel()
	}
	waitErr := cmd.Wait()
	if cmd.ProcessState == nil {
		return ErrProcessExecutionFailed
	}
	state.Reaped = true
	state.Process.ExitCode = cmd.ProcessState.ExitCode()
	state.Process.FinishedAtMS = time.Now().UnixMilli()
	state.Process.DurationMS = max(1, time.Since(started).Milliseconds())
	state.Phase = "reaped"
	if saveErr != nil || waitErr != nil || ctx.Err() != nil {
		return errors.Join(ErrProcessExecutionFailed, saveErr, ctx.Err())
	}
	if err := state.Process.Validate(); err != nil {
		return err
	}
	return offlineSave(root, *state)
}

// InspectOfflineCleanup checks a completed operation against its current input
// and output. Caller-owned files remain untrusted diagnostics, even on success.
func InspectOfflineCleanup(ctx context.Context, workspace, operationID string) (*OfflineCleanupObservation, error) {
	if !offlineSupported() {
		return nil, ErrOfflinePlatform
	}
	root, err := offlinePath(workspace)
	if err != nil {
		return nil, ErrOfflineState
	}
	lock, err := acquireOfflineLock(filepath.Join(root, ".operation.lock"))
	if err != nil {
		return nil, err
	}
	defer lock.close()
	return offlineInspectChecked(ctx, root, operationID)
}

// RecoverOfflineCleanup only tombstones an abandoned nonterminal operation.
// It never launches, kills a persisted PID, publishes output or mints evidence.
// A still-running orphan retains the inherited lock and rejects recovery.
func RecoverOfflineCleanup(workspace, operationID string) error {
	if !offlineSupported() {
		return ErrOfflinePlatform
	}
	root, err := offlinePath(workspace)
	if err != nil {
		return ErrOfflineState
	}
	lock, err := acquireOfflineLock(filepath.Join(root, ".operation.lock"))
	if err != nil {
		return err
	}
	defer lock.close()
	_, state, err := offlineLoad(root, operationID)
	if err != nil {
		return err
	}
	if state.Phase == "sealed" || state.Phase == "failed" || state.Phase == "interrupted" {
		return ErrOfflineReuse
	}
	state.Phase, state.OutputManifestDigest = "interrupted", ""
	return offlineSave(root, state)
}

func offlineInspectChecked(ctx context.Context, root, id string) (*OfflineCleanupObservation, error) {
	intent, state, err := offlineLoad(root, id)
	if err != nil {
		return nil, err
	}
	if state.Phase != "sealed" || !state.Reaped || state.Process.Validate() != nil || !offlineDigest(state.OutputManifestDigest) {
		return nil, ErrOfflineState
	}
	if _, err := offlineInput(ctx, intent.InputBundle, intent.InputManifestDigest); err != nil {
		return nil, err
	}
	m, err := maintenancebundle.Verify(ctx, filepath.Join(root, "output"))
	if err != nil {
		return nil, ErrBundleMismatch
	}
	d, err := ComputeManifestDigest(m)
	if err != nil || d != state.OutputManifestDigest {
		return nil, ErrBundleMismatch
	}
	l := maintenancebundle.BundleLayout(filepath.Join(root, "output"))
	// A sealed bundle has no place for a process lock without changing its
	// manifest. Restore it into a private owner-controlled root and hold the
	// normal Manager lock while rechecking source semantics and checkpoints.
	inspectionParent, err := os.MkdirTemp(root, ".offline-output-inspection-")
	if err != nil {
		return nil, ErrInvalidSource
	}
	defer os.RemoveAll(inspectionParent)
	inspection, err := maintenancebundle.RestoreNew(ctx, filepath.Join(root, "output"), filepath.Join(inspectionParent, "restored"))
	if err != nil {
		return nil, ErrBundleMismatch
	}
	inspectionLock, err := processlock.Acquire(inspection.DatabasePath)
	if err != nil {
		return nil, ErrOfflineBusy
	}
	defer inspectionLock.Close()
	if err := offlineSourceObserved(ctx, inspection, inspectionLock); err != nil {
		return nil, err
	}
	checkpoints, err := inspectCheckpoints(ctx, inspection.DatabasePath, inspectionLock)
	if err != nil || checkpoints != state.Checkpoints {
		return nil, ErrCheckpointFailed
	}
	if err := maintenancebundle.Revalidate(ctx, inspection, m); err != nil {
		return nil, ErrBundleMismatch
	}
	if err := maintenancebundle.Revalidate(ctx, l, m); err != nil {
		return nil, ErrBundleMismatch
	}
	if _, err := offlineInput(ctx, intent.InputBundle, intent.InputManifestDigest); err != nil {
		return nil, err
	}
	currentIntent, currentState, err := offlineLoad(root, id)
	if err != nil || !reflect.DeepEqual(currentIntent, intent) || !reflect.DeepEqual(currentState, state) {
		return nil, ErrOfflineState
	}
	return &OfflineCleanupObservation{Status: "observed-offline-cleanup-only", Tool: intent.Tool, OperationID: id, Release: intent.Release, SourceCommit: intent.SourceCommit, Asset: intent.Asset, ArchiveSHA256: intent.ArchiveSHA256, ExecutableSHA256: intent.ExecutableSHA256, InputManifestDigest: intent.InputManifestDigest, OutputManifestDigest: state.OutputManifestDigest, Process: state.Process, Reaped: state.Reaped, Checkpoints: checkpoints}, nil
}

func offlineSource(ctx context.Context, l maintenancebundle.Layout) error {
	root := filepath.Dir(l.DatabasePath)
	manifestPath := filepath.Join(root, "manifest.json")
	if _, err := os.Lstat(manifestPath); err == nil {
		// A sealed bundle cannot be locked in place: adding its .manager.lock
		// would change the manifest inventory. Restore a fenced inspection copy
		// and validate the copy under the normal Manager ownership lock instead.
		parent := filepath.Dir(root)
		tmpParent, err := os.MkdirTemp(parent, ".offline-source-inspection-")
		if err != nil {
			return ErrInvalidSource
		}
		defer os.RemoveAll(tmpParent)
		tmp := filepath.Join(tmpParent, "restored")
		copyLayout, err := maintenancebundle.RestoreNew(ctx, root, tmp)
		if err != nil {
			return ErrInvalidSource
		}
		lock, err := processlock.Acquire(copyLayout.DatabasePath)
		if err != nil {
			return ErrInvalidSource
		}
		defer lock.Close()
		return offlineSourceObserved(ctx, copyLayout, lock)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrInvalidSource
	}
	lock, err := processlock.Acquire(l.DatabasePath)
	if err != nil {
		return ErrInvalidSource
	}
	defer lock.Close()
	return offlineSourceObserved(ctx, l, lock)
}

func offlineSourceObserved(ctx context.Context, l maintenancebundle.Layout, lock *processlock.Lock) error {
	o, err := sqlite.InspectLegacySource(ctx, sqlite.InspectionPaths{Database: l.DatabasePath, DataKey: l.DataKeyPath, Config: l.ConfigPath, Archives: l.ArchivesPath}, sqlite.InspectionOptions{SourceStopped: true, ManagerLock: lock})
	if err != nil || len(o.Issues) > 0 || o.SQLite != "observed" {
		return ErrInvalidSource
	}
	p := legacyprofile.Classify(o)
	if p.Status != "recognized" || (p.Family != "LegacySchemaA" && p.Family != "LegacySchemaB" && p.Family != "LegacySchemaC") || o.History["maintenance"] != "none-observed" || o.History["archives"] == "pending-observed" {
		return ErrInvalidSource
	}
	return nil
}

func offlineInput(ctx context.Context, root, digest string) (maintenancebundle.Manifest, error) {
	m, err := maintenancebundle.Verify(ctx, root)
	if err != nil {
		return maintenancebundle.Manifest{}, ErrOfflineInputs
	}
	d, err := ComputeManifestDigest(m)
	if err != nil || d != digest {
		return maintenancebundle.Manifest{}, ErrOfflineInputs
	}
	return m, nil
}

func offlineExecutable(ctx context.Context, path, digest string) error {
	f, err := offlineOpen(path, 0o700)
	if err != nil {
		return ErrExecutableMismatch
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, &contextReader{ctx: ctx, r: io.LimitReader(f, maxExecutableBytes+1)}); err != nil || hex.EncodeToString(h.Sum(nil)) != digest {
		return ErrExecutableMismatch
	}
	return nil
}

func offlineLoad(root, id string) (offlineIntent, offlineState, error) {
	var in offlineIntent
	var state offlineState
	if len(id) != 32 {
		return in, state, ErrOfflineState
	}
	if _, err := hex.DecodeString(id); err != nil {
		return in, state, ErrOfflineState
	}
	data, err := offlineRead(filepath.Join(root, "intent.json"), 0o400, &in)
	if err != nil {
		return in, state, err
	}
	if _, err := offlineRead(filepath.Join(root, "state.json"), 0o600, &state); err != nil {
		return in, state, err
	}
	pin, ok := nativePins[in.Asset]
	if !ok || in.Tool != offlineTool || in.OperationID != id || state.OperationID != id || in.Release != Release || in.SourceCommit != SourceCommit || in.ArchiveSHA256 != pin.ArchiveSHA256 || in.ExecutableSHA256 != pin.ExecutableSHA256 || !offlineDigest(in.InputManifestDigest) || in.TimeoutMS <= 0 || in.TimeoutMS > int64((10*time.Minute).Milliseconds()) || state.IntentDigest != offlineHash(data) {
		return in, state, ErrOfflineState
	}
	p, err := offlinePath(in.InputBundle)
	if err != nil || p != in.InputBundle || offlineOverlap(p, root) {
		return in, state, ErrOfflineState
	}
	switch state.Phase {
	case "prepared", "running", "reaped", "sealed", "failed", "interrupted":
	default:
		return in, state, ErrOfflineState
	}
	return in, state, nil
}

func offlineCheckIntent(root string, expected []byte, state offlineState) error {
	var in offlineIntent
	data, err := offlineRead(filepath.Join(root, "intent.json"), 0o400, &in)
	if err != nil || !bytes.Equal(data, expected) {
		return ErrOfflineState
	}
	var current offlineState
	if _, err := offlineRead(filepath.Join(root, "state.json"), 0o600, &current); err != nil {
		return err
	}
	if !reflect.DeepEqual(current, state) {
		return ErrOfflineState
	}
	return nil
}

func offlineSave(root string, state offlineState) error {
	data, err := offlineJSON(state)
	if err != nil {
		return ErrOfflineState
	}
	tmp, err := os.CreateTemp(root, ".state-")
	if err != nil {
		return ErrOfflineState
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(root, "state.json"))
	}
	if err != nil {
		return ErrOfflineState
	}
	return offlineSync(root)
}

func offlineJSON(v any) ([]byte, error) { data, err := json.Marshal(v); return append(data, '\n'), err }
func offlineHash(data []byte) string    { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func offlineDigest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}

func offlineRead(path string, mode os.FileMode, v any) ([]byte, error) {
	f, err := offlineOpen(path, mode)
	if err != nil {
		return nil, ErrOfflineState
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return nil, ErrOfflineState
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return nil, ErrOfflineState
	}
	canonical, err := offlineJSON(v)
	if err != nil || !bytes.Equal(canonical, data) {
		return nil, ErrOfflineState
	}
	return data, nil
}

func offlineOpen(path string, mode os.FileMode) (*os.File, error) {
	if _, err := offlinePath(path); err != nil {
		return nil, err
	}
	if err := checkPrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		return nil, ErrOfflineState
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !offlineSingleLink(f) {
		f.Close()
		return nil, ErrOfflineState
	}
	return f, nil
}

func offlineWriteNew(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	return offlineSync(filepath.Dir(path))
}
func offlineSync(root string) error {
	f, err := os.Open(root)
	if err != nil {
		return ErrOfflineState
	}
	err = errors.Join(f.Sync(), f.Close())
	if err != nil {
		return ErrOfflineState
	}
	return nil
}
func offlinePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", ErrOfflineInputs
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for cur := p; ; cur = filepath.Dir(cur) {
		info, err := os.Lstat(cur)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || cur != p && !info.IsDir()) {
			return "", ErrOfflineInputs
		}
		if cur == filepath.Dir(cur) {
			break
		}
	}
	return p, nil
}
func offlineOverlap(a, b string) bool {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		r, err := filepath.Rel(pair[0], pair[1])
		if err == nil && (r == "." || r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))) {
			return true
		}
	}
	return false
}
