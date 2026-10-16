// Package bootstrapdiscovery projects safe, no-migrate inspection evidence
// into the deliberately small unauthenticated discovery contract. It has no
// writable store dependency and cannot grant native startup admission.
package bootstrapdiscovery

import (
	"encoding/json"
	"net/http"
	"strings"

	bootstrap "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

const (
	// Path is the sole temporary unauthenticated Bootstrap discovery path.
	Path = "/bootstrap/discovery"
	// HealthPath is the existing fixed liveness transport. During pre-write
	// startup it reports process liveness only and never product readiness.
	HealthPath = "/health"
)

type Reason string

const (
	ReasonFreshClaimRequired          Reason = "fresh_claim_required"
	ReasonAdminAuthenticationRequired Reason = "admin_authentication_required"
	ReasonUnsupportedUpgrade          Reason = "unsupported_upgrade"
	ReasonRecoveryRequired            Reason = "recovery_required"
	ReasonHostInterventionRequired    Reason = "host_intervention_required"
	ReasonQualificationUnavailable    Reason = "qualification_unavailable"
	ReasonQualificationUnknown        Reason = "qualification_unknown"
	ReasonUnavailable                 Reason = "unavailable"
)

type Result struct {
	Available              bool           `json:"available"`
	Mode                   bootstrap.Mode `json:"mode,omitempty"`
	Reason                 Reason         `json:"reason"`
	AuthenticationRequired bool           `json:"authenticationRequired"`
	RecoveryRequired       bool           `json:"recoveryRequired"`
}

// Input keeps transport projection explicit. InspectionError is intentionally
// retained: an unstable/unsafe observation cannot be converted into Recovery.
type Input struct {
	Report          legacypreflight.Report
	InspectionError error
}

func Unavailable(reason Reason) Result {
	if reason == "" {
		reason = ReasonUnavailable
	}
	return Result{Reason: reason}
}

func Project(input Input) Result {
	if input.InspectionError != nil {
		return Unavailable(ReasonUnavailable)
	}
	if !safeObservation(input.Report) {
		return Unavailable(ReasonQualificationUnknown)
	}

	admin := bootstrap.AdminCredentialUninitialized
	switch input.Report.Observed.BootstrapState.AdminCredential {
	case "initialized":
		admin = bootstrap.AdminCredentialInitialized
	case "unverifiable":
		admin = bootstrap.AdminCredentialUnverifiable
	}
	sourceKind := strings.TrimSpace(input.Report.SourceKind)
	if sourceKind == "native-v2" {
		// A same-schema source still lacks trusted exact-release authority. It
		// may be writable under the existing native gate, but that gate is not
		// a Bootstrap discovery qualification grant and cannot imply completed.
		if admin != bootstrap.AdminCredentialInitialized {
			return Result{Available: true, Mode: bootstrap.Recovery, Reason: ReasonHostInterventionRequired, RecoveryRequired: true}
		}
		return Result{Available: true, Mode: bootstrap.UnsupportedUpgrade, Reason: ReasonQualificationUnavailable, AuthenticationRequired: true}
	}
	if sourceKind == "empty-database" || sourceKind == "partial-source" {
		return Result{Available: true, Mode: bootstrap.Recovery, Reason: ReasonHostInterventionRequired, RecoveryRequired: true}
	}

	evidence := bootstrap.ClassificationEvidence{
		HasV1ProductState: sourceKind == "legacy-manager",
		AdminCredential:   admin,
		Session:           bootstrap.SessionMissing,
		RequiredCheckpoints: bootstrap.RequiredMigrationCheckpoints{
			CacheAccounting: bootstrap.CheckpointPending,
			QuotaSnapshot:   bootstrap.CheckpointPending,
		},
	}
	mode, err := bootstrap.Classify(evidence)
	if err != nil {
		return Unavailable(ReasonQualificationUnknown)
	}
	switch mode {
	case bootstrap.Fresh:
		return Result{Available: true, Mode: mode, Reason: ReasonFreshClaimRequired}
	case bootstrap.UnsupportedUpgrade:
		return Result{Available: true, Mode: mode, Reason: ReasonQualificationUnavailable, AuthenticationRequired: true}
	case bootstrap.Recovery:
		return Result{Available: true, Mode: mode, Reason: ReasonHostInterventionRequired, RecoveryRequired: true}
	case bootstrap.Completed:
		// Completed is a fixed-route terminal state, but this lane never
		// infers it from legacy state. Keep the explicit behavior for callers
		// that provide a future authoritative result.
		return Result{Available: true, Mode: mode}
	default:
		return Unavailable(ReasonQualificationUnavailable)
	}
}

func safeObservation(report legacypreflight.Report) bool {
	if report.SourceKind == "fresh" {
		return len(report.Observed.Files) > 0 && report.Observed.Files[0].Role == "database" &&
			report.Observed.Files[0].State == "missing" && len(report.Observed.Issues) == 0 &&
			report.Observed.SQLite == "not-inspected" && report.Observed.BootstrapState.Valid &&
			!report.Observed.BootstrapState.Present && !report.Observed.BootstrapState.AdminCredentialPresent &&
			report.Observed.BootstrapState.AdminCredential == "uninitialized"
	}
	if report.SourceKind != "legacy-manager" && report.SourceKind != "native-v2" && report.SourceKind != "empty-database" && report.SourceKind != "partial-source" {
		return false
	}
	if len(report.Observed.Files) == 0 || report.Observed.Consistency == "changed" {
		return false
	}
	if report.SourceKind == "legacy-manager" || report.SourceKind == "native-v2" {
		if report.Observed.SQLite != "observed" || len(report.Observed.Issues) != 0 ||
			!stableConsistency(report.Observed.Consistency) || !report.Observed.BootstrapState.Valid {
			return false
		}
		if report.Observed.BootstrapState.AdminCredential == "initialized" && !report.Observed.BootstrapState.AdminCredentialPresent {
			return false
		}
		switch report.Observed.BootstrapState.AdminCredential {
		case "", "uninitialized", "initialized", "unverifiable":
			return true
		default:
			return false
		}
	}
	return report.Observed.SQLite == "not-inspected" && len(report.Observed.Issues) > 0
}

func stableConsistency(value string) bool {
	switch value {
	case "operator-asserted-cold", "manager-fenced-cold", "manager-fenced-recovery-copy":
		return true
	default:
		return false
	}
}

// Handler serves only the fixed discovery/liveness transport. It deliberately
// does not expose configured paths, source details, or legacy state.
type Handler struct {
	Result Result
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case HealthPath:
		h.handleHealth(w, r)
	case Path:
		h.handleDiscovery(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "phase": "bootstrap"})
}

func (h Handler) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	if h.Result.Mode == bootstrap.Completed {
		// Ownership remains captured, but completed never discloses state.
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !h.Result.Available {
		writeJSON(w, http.StatusServiceUnavailable, Result{Reason: h.Result.Reason})
		return
	}
	writeJSON(w, http.StatusOK, h.Result)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
