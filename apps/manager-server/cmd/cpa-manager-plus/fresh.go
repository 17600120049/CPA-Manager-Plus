package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/buildinfo"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/routing"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/freshbootstrap"
	healthcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/health"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/httpapi"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	keyrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshstore"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/bootstrapdiscovery"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeyapply"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshrouting"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshstrategy"
	runtimeservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/runtime"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/updatecheck"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// initialize is a host-owned offline operation. It admits the actual store and
// returns a bounded fragment handoff to its caller, never a service log or a
// second long-running daemon. The normal service starts/resumes the same store.
func runFreshInitialize(ctx context.Context, stdout io.Writer) error {
	cfg, err := config.LoadForInspection()
	if err != nil {
		return err
	}
	if err := validateFreshHostAuthority(cfg); err != nil {
		return err
	}
	paths, err := freshPaths(cfg)
	if err != nil {
		return err
	}
	opened, err := fresh.Open(ctx, paths, fresh.OpenOptions{Initialize: true, AdminKey: cfg.AdminKey, DataKey: cfg.DataKey})
	if err != nil {
		return err
	}
	defer opened.Close()
	handoff, err := fresh.New(freshstore.New(opened.DB)).Issue(ctx)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, bootstrap.FixedBootstrapUIPath+handoff.Fragment+"\n")
	return err
}

func freshPaths(cfg config.Config) (sqliterepo.InspectionPaths, error) {
	configPath, err := config.InspectionConfigPath()
	return sqliterepo.InspectionPaths{Database: cfg.DBPath, Config: configPath, DataKey: cfg.DataKeyPath, Archives: cfg.UsageArchiveDir}, err
}

var errFreshExternalPermitRevoked = errors.New("fresh external host permit revoked")

type freshApplication struct {
	store          freshstore.Store
	authority      *fresh.Service
	routing        *freshrouting.Service
	strategy       *freshstrategy.Service
	runtime        runtimeservice.RuntimeClient
	externalPermit func(bootstrap.FreshOperation, uint64, string, string, string) bool
	protector      *security.Protector
	fixed          http.Handler
	keys           *freshkeys.Service
	keyApply       *freshkeyapply.Service
	updates        *updatecheck.Service
	configRetry    *freshconfig.Service
	pahS0          bool
}

func newFreshApplication(opened *fresh.Opened, cfg config.Config, driver freshrouting.Driver) *freshApplication {
	st := freshstore.New(opened.DB)
	a := &freshApplication{store: st, authority: fresh.New(st), routing: freshrouting.New(st, driver), protector: opened.Protector, pahS0: cfg.PAHS0Enabled}
	a.keys = freshkeys.New(keyrepo.New(opened.DB), opened.Protector)
	// Reuse the existing settings authority without opening a Legacy app,
	// collector, CPA client, or second SQLite connection.
	a.updates = updatecheck.New(store.New(opened.DB), buildinfo.Version, buildinfo.SourceCommit, os.Getenv("CPAMP_UPDATE_CHECK_ENABLED") != "false")
	var runtime runtimeservice.RuntimeClient
	if cfg.EmbeddedRuntimeConfigured() {
		runtime = runtimeservice.NewEmbeddedClientWithTokenSource(cfg.RuntimeURL, runtimeservice.NewFileRuntimeTokenSource(cfg.RuntimeTokenFile))
	}
	// The private host launcher alone mounts the short-lived permit. The default
	// v7 profile and existing environments never opt into this consumer.
	h1Mode := os.Getenv("CPAMP_PRIVATE_H1")
	h1Valid := h1Mode == "" || (h1Mode == "1" && os.Getenv("CPAMP_PRIVATE_CONFIG_AUTHORITY") == "1" && os.Getenv("CPAMP_PRIVATE_KEY_OPERATIONS") == "1")
	if h1Valid && os.Getenv("CPAMP_PRIVATE_S1_PERMIT") == freshkeyapply.PermitPath && cfg.RuntimeURL == "http://127.0.0.1:9081" && cfg.CPAUpstreamURL == "http://127.0.0.1:8317" {
		a.keyApply = freshkeyapply.New(keyrepo.New(opened.DB), a.keys, runtime)
		a.configRetry = freshconfig.New(st, a.keyApply.ConfigTarget)
		if os.Getenv("CPAMP_PRIVATE_H1") == "1" && os.Getenv("CPAMP_PRIVATE_CONFIG_AUTHORITY") == "1" && os.Getenv("CPAMP_PRIVATE_KEY_OPERATIONS") == "1" {
			h := cpa.NewH1Coordinator(opened.DB)
			a.keyApply.EnableH1(h)
			a.configRetry.EnableH1(h)
		}
	}
	// Only the build-bound consumer record can grant production qualification.
	// Pending/missing evidence retains the existing typed strategy blockers.
	a.strategy = freshstrategy.NewProduction(freshstrategy.Options{Runtime: runtime, Protector: opened.Protector, DestinationBaseURL: cfg.CPAUpstreamURL})
	a.runtime = runtime
	a.externalPermit = privateFreshExternalAdmitted
	a.fixed = freshbootstrap.Handler{Authority: a.authority, Routing: a.routing, Current: a.current,
		Shell: httpapi.BootstrapWebHandler(), Strategy: http.HandlerFunc(a.fixedStrategy)}
	return a
}

func (a *freshApplication) current(ctx context.Context) (bootstrap.FreshOperation, error) {
	r, err := a.store.Load(ctx)
	return r.Operation, err
}

func writeFreshStrategyError(w http.ResponseWriter, err error) {
	var blocker *freshstrategy.Blocker
	if errors.As(err, &blocker) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		writeFreshJSON(w, blocker)
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
}

func (a *freshApplication) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r != nil && r.URL != nil && r.URL.Path == bootstrap.FixedLivenessPath {
		// Fresh store takeover makes the Manager ready, not the installation or
		// Gateway. Re-read the validated store so a later failure is not healthy.
		if _, err := a.current(r.Context()); err != nil {
			bootstrapdiscovery.NotReadyHandler{}.ServeHTTP(w, r)
		} else {
			handler := healthcontroller.Handler{ServiceID: "cpa-manager-plus"}
			handler.Health(w, r)
		}
		return
	}
	if r != nil && r.URL != nil && r.URL.Path == privateChatAdmissionPath {
		a.privateChatAdmission(w, r)
		return
	}
	if r != nil && r.URL != nil && r.URL.Path == freshrouting.AuthorityPath && r.URL.EscapedPath() == r.URL.Path && r.URL.RawQuery == "" {
		a.routingAuthority(w, r)
		return
	}
	if r != nil && r.URL != nil && r.URL.Path == privateStagedKeyProofPath {
		a.privateStagedKeyProof(w, r)
		return
	}
	protect := routing.ProtectFixedBootstrap(http.HandlerFunc(a.canonical), a.fixed)
	httpapi.ProtectBrandResources(protect).ServeHTTP(w, r)
}

func (a *freshApplication) routingAuthority(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	op, err := a.current(r.Context())
	if err != nil || op.Routing.Target == "" || op.Routing.OperationID == "" {
		http.NotFound(w, r)
		return
	}
	writeFreshJSON(w, freshrouting.StateFor(op))
}

func (a *freshApplication) canonical(w http.ResponseWriter, r *http.Request) {
	op, err := a.current(r.Context())
	if err != nil || op.Routing.Target == "" {
		http.NotFound(w, r)
		return
	}
	api := http.HandlerFunc(a.canonicalAPI)
	if op.Routing.Committed {
		snapshot := a.routing.Snapshot(r.Context())
		if !snapshot.PublicAPIActive() {
			http.NotFound(w, r)
			return
		}
		httpapi.FullWebHandler(snapshot, api).ServeHTTP(w, r)
		return
	}
	// The pending route is explicitly owned by the existing Fresh checkpoint;
	// it is a verification candidate and never a verified-effective snapshot.
	httpapi.InitialFullWebHandler(op.Routing.Target, api).ServeHTTP(w, r)
}

func (a *freshApplication) canonicalAPI(w http.ResponseWriter, r *http.Request) {
	// Full Web's HTML/static wrapper sets no-cache. Restore the API boundary
	// here before any authentication error or one-time secret response.
	w.Header().Set("Cache-Control", "no-store")
	record, err := a.store.Load(r.Context())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	op := record.Operation
	base := op.Routing.Target + routing.PublicAPIV2Suffix + "/"
	resource := strings.TrimPrefix(r.URL.Path, base)
	if resource == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	// Non-secret identity is the bounded real-public-entry verification leaf.
	// It authenticates no browser and cannot mutate the pending installation.
	if resource == "bootstrap/identity" && r.Method == http.MethodGet {
		writeFreshJSON(w, freshrouting.StateFor(op))
		return
	}
	if !op.Routing.Committed || record.Admin == nil || !security.VerifyAdminKey(*record.Admin, security.ExtractBearerToken(r.Header.Get("Authorization"))) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case resource == "gateway/retry/request-retry" || resource == "gateway/retry/max-retry-interval" || strings.HasPrefix(resource, "gateway/retry/operations/") || strings.HasPrefix(resource, "gateway/retry/interval-operations/"):
		a.retryConfiguration(w, r, resource)
	case resource == "settings/update-channel":
		a.updateChannel(w, r, op)
	case resource == "api-keys" || strings.HasPrefix(resource, "api-keys/"):
		a.clientKeys(w, r, op, resource)
	case resource == "operation" && r.Method == http.MethodGet:
		writeFreshJSON(w, op)
	case resource == "installation/runtime" && r.Method == http.MethodGet:
		a.installationRuntime(w, r, op)
	case resource == "strategy" && r.Method == http.MethodPost:
		a.executeStrategy(w, r, op)
	case resource == "complete" && r.Method == http.MethodPost:
		// External is a connection checkpoint, not an activated product.
		if op.Strategy.Strategy == bootstrap.ConnectExternal || op.Strategy.Strategy == bootstrap.AdoptExisting {
			// Neither External nor Adopt has ProductComplete authorization.
			w.WriteHeader(http.StatusConflict)
			return
		}
		if op.ProductComplete {
			writeFreshJSON(w, op)
			return
		}
		if !a.managedInstallationReady(r.Context(), op) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		next, err := a.store.Update(r.Context(), op.Identity, op.Revision, func(record *freshstore.Record) error {
			if record.Operation.Strategy.Stage != "committed" {
				return freshstore.ErrConflict
			}
			record.Operation.ProductComplete = true
			return nil
		})
		if err != nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		writeFreshJSON(w, next.Operation)
	default:
		http.NotFound(w, r)
	}
}

func (a *freshApplication) fixedStrategy(w http.ResponseWriter, r *http.Request) {
	token, ok := freshbootstrap.SessionToken(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	op, err := a.authority.Authorize(r.Context(), token)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	a.executeStrategy(w, r, op)
}

func (a *freshApplication) executeStrategy(w http.ResponseWriter, r *http.Request, op bootstrap.FreshOperation) {
	var request struct {
		Identity         bootstrap.FreshIdentity `json:"identity"`
		ExpectedRevision uint64                  `json:"expectedRevision"`
		AttemptID        string                  `json:"attemptId,omitempty"`
		freshstrategy.Request
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil || request.Identity != op.Identity {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Accepted CPA source/destination qualification is not Host takeover
	// authority. Fresh Adopt has no approved production writer or Host permit.
	// Keep both canonical and fixed Bootstrap fail-closed before source GET,
	// Runtime stage/start or any Manager checkpoint mutation.
	if request.Strategy == bootstrap.AdoptExisting {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	// Private P-S0 denies External/Adopt before Host permit,
	// durable reservation, network source read and Fresh CAS mutation.
	if a.pahS0 {
		if err := validatePAHS0Strategy(op.Strategy.Strategy); err != nil {
			writeFreshStrategyError(w, err)
			return
		}
		if err := requirePAHS0ManagedRequest(request.Strategy); err != nil {
			writeFreshStrategyError(w, err)
			return
		}
	}
	if request.ExpectedRevision != op.Revision {
		// A stale External CAS is a conflict, not an invalid user-supplied target.
		if request.Strategy == bootstrap.ConnectExternal {
			w.WriteHeader(http.StatusConflict)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		return
	}
	var externalBaseURL string
	if request.Strategy == bootstrap.ConnectExternal {
		// Replays and inadmissible input must never reserve the only Fresh attempt.
		if op.Strategy.Strategy != "" {
			w.WriteHeader(http.StatusConflict)
			return
		}
		// Reuse the same compiled consumer qualification and static input rules
		// as Execute, without performing any CPA network request.
		var preflightErr error
		externalBaseURL, preflightErr = a.strategy.PreflightExternal(request.Request)
		if preflightErr != nil {
			writeFreshStrategyError(w, preflightErr)
			return
		}
		// The browser-generated attempt ID is ephemeral and never in GET/store.
		// A new tab/refresh must not consume a Host permit for the first POST.
		if !validFreshExternalAttemptID(request.AttemptID) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// A Host grant binds the original revision, URL, Key, and request ID.
		// Only the original per-page attempt can claim an empty checkpoint.
		if a.externalPermit == nil || !a.externalPermit(op, request.ExpectedRevision, externalBaseURL, request.ManagementKey, request.AttemptID) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		digest := sha256.Sum256([]byte(externalBaseURL))
		target := "external:sha256:" + hex.EncodeToString(digest[:])
		// Persist an exclusive durable attempt fence BEFORE contacting CPA.
		// validated without a stored connection is an intentionally terminal
		// Host-reconciliation state if the source read or reply is lost.
		claimed, err := a.store.Update(r.Context(), op.Identity, op.Revision, func(record *freshstore.Record) error {
			if record.Operation.Strategy.Strategy != "" {
				return freshstore.ErrConflict
			}
			if !a.externalPermit(record.Operation, request.ExpectedRevision, externalBaseURL, request.ManagementKey, request.AttemptID) {
				return errFreshExternalPermitRevoked
			}
			if token, fixed := freshbootstrap.SessionToken(r); fixed {
				if err := a.authority.ValidateSession(*record, token); err != nil {
					return err
				}
			}
			record.Operation.Strategy = bootstrap.FreshStrategyCheckpoint{
				Strategy: bootstrap.ConnectExternal, TargetIdentity: target,
				TargetGeneration: 1, Stage: "validated",
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, errFreshExternalPermitRevoked) {
				w.WriteHeader(http.StatusServiceUnavailable)
			} else {
				w.WriteHeader(http.StatusConflict)
			}
			return
		}
		op = claimed.Operation
		if !a.externalPermit(op, request.ExpectedRevision, externalBaseURL, request.ManagementKey, request.AttemptID) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}
	if request.Strategy == bootstrap.AdoptExisting {
		record, err := a.store.Load(r.Context())
		if err != nil || record.Operation.Identity != op.Identity || record.Operation.Revision != op.Revision {
			w.WriteHeader(http.StatusConflict)
			return
		}
		request.Request.AdoptionCiphertext = record.AdoptionCiphertext
	}
	result, err := a.strategy.Execute(r.Context(), op, request.Request)
	if err != nil {
		writeFreshStrategyError(w, err)
		return
	}
	if request.Strategy == bootstrap.ConnectExternal && !a.externalPermit(op, request.ExpectedRevision, externalBaseURL, request.ManagementKey, request.AttemptID) {
		// A revoked/expired Host permit cannot complete a previously claimed attempt.
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if token, fixed := freshbootstrap.SessionToken(r); fixed {
		current, err := a.authority.Authorize(r.Context(), token)
		if err != nil || current.Identity != op.Identity || current.Revision != op.Revision {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	if op.Strategy.Stage == "committed" {
		// Reconcile only after the strategy adapter validates the requested
		// target and current server-owned qualification. Do not rewrite a
		// committed checkpoint, connection ciphertext or revision on replay.
		writeFreshJSON(w, op)
		return
	}
	next, err := a.store.Update(r.Context(), op.Identity, op.Revision, func(record *freshstore.Record) error {
		if request.Strategy == bootstrap.ConnectExternal && !a.externalPermit(record.Operation, request.ExpectedRevision, externalBaseURL, request.ManagementKey, request.AttemptID) {
			return errFreshExternalPermitRevoked
		}
		if token, fixed := freshbootstrap.SessionToken(r); fixed {
			if err := a.authority.ValidateSession(*record, token); err != nil {
				return err
			}
		}
		record.Operation.Strategy = result.Checkpoint
		if result.AdoptionCiphertext != "" {
			record.AdoptionCiphertext = result.AdoptionCiphertext
		}
		if result.Connection != nil {
			record.Connection = &model.ManagerCPAConnectionConfig{CPABaseURL: result.Connection.BaseURL, ManagementKey: result.Connection.ManagementKeyCiphertext}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errFreshExternalPermitRevoked) {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusConflict)
		}
		return
	}
	writeFreshJSON(w, next.Operation)
}

func writeFreshJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func freshDriver(cfg config.Config) freshrouting.Driver {
	driver, err := freshrouting.NewHTTPDriver(freshrouting.HTTPConfig{
		IngressControlURL: strings.TrimSpace(os.Getenv("CPAMP_INGRESS_CONTROL_URL")),
		PublicOrigin:      strings.TrimSpace(os.Getenv("CPAMP_PUBLIC_ORIGIN")),
	})
	if err != nil {
		return nil
	}
	return driver
}

func restoreFreshRouting(ctx context.Context, service *freshrouting.Service) {
	// Ingress is stateless. Re-deliver only the original persisted projection
	// after an independent Ingress restart; this never advances DB authority.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = service.Restore(probe)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func freshMarkerPresent(database string) bool {
	info, err := os.Lstat(database + ".manager.lock")
	// An ordinary historical empty marker is not Fresh evidence. Any bounded
	// non-empty marker requires strict parsing by admission and fails closed.
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func serveExistingFresh(ctx context.Context, cfg config.Config, paths sqliterepo.InspectionPaths, h *startupHTTPHandler) (*fresh.Opened, error) {
	if err := validateFreshHostAuthority(cfg); err != nil {
		return nil, err
	}
	opened, err := fresh.Open(ctx, paths, fresh.OpenOptions{AdminKey: cfg.AdminKey, DataKey: cfg.DataKey})
	if err != nil {
		return nil, err
	}
	if cfg.PAHS0Enabled {
		// The Fresh marker does not authorize External or Adopt.
		record, checkErr := freshstore.New(opened.DB).Load(ctx)
		if checkErr == nil {
			checkErr = validatePAHS0Strategy(record.Operation.Strategy.Strategy)
		}
		if checkErr != nil {
			_ = opened.Close()
			return nil, checkErr
		}
	}
	app := newFreshApplication(opened, cfg, freshDriver(cfg))
	h.SetFreshHandler(app)
	go restoreFreshRouting(ctx, app.routing)
	go app.runFreshUpdateChecks(ctx)
	return opened, nil
}

func validateFreshHostAuthority(cfg config.Config) error {
	if cfg.AdminAuthorityConfigured && cfg.AdminKey == "" {
		return fresh.ErrAuthority
	}
	return nil
}

func awaitFreshStore(ctx context.Context, cfg config.Config, paths sqliterepo.InspectionPaths, h *startupHTTPHandler) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !freshMarkerPresent(paths.Database) {
			continue
		}
		opened, err := serveExistingFresh(ctx, cfg, paths, h)
		if err != nil {
			continue
		}
		<-ctx.Done()
		_ = opened.Close()
		return
	}
}
