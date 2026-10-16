package bootstrap

import (
	"errors"
	"fmt"
	"strings"
)

type RouteMethodClass string

const (
	RouteMethodAll       RouteMethodClass = "all"
	RouteMethodReadOnly  RouteMethodClass = "get_options"
	RouteMethodNamespace RouteMethodClass = "namespace"
	RouteMethodLiveness  RouteMethodClass = "liveness"
	RouteMethodStatic    RouteMethodClass = "static"
)

type RouteLifecycle string

const (
	RouteLifecycleActive   RouteLifecycle = "active"
	RouteLifecycleTerminal RouteLifecycle = "terminal"
	RouteLifecycleRetired  RouteLifecycle = "retired"
)

type RouteOwnerClass string

const (
	OwnerBootstrapDiscovery RouteOwnerClass = "fixed_bootstrap_discovery"
	OwnerPublicAPIV2        RouteOwnerClass = "canonical_public_api_v2"
	OwnerManagerControl     RouteOwnerClass = "manager_control"
	OwnerGatewayDataPlane   RouteOwnerClass = "gateway_data_plane"
	OwnerFullWebStatic      RouteOwnerClass = "full_web_static_spa"
	OwnerPanelLite          RouteOwnerClass = "panel_lite"
	OwnerFixedTransport     RouteOwnerClass = "fixed_transport_liveness"
	OwnerRetiredControlDeny RouteOwnerClass = "retired_control_deny_only"
)

type RouteAuthClass string

const (
	RouteAuthUnauthenticated RouteAuthClass = "unauthenticated"
	RouteAuthManagerSession  RouteAuthClass = "manager_session"
	RouteAuthManagerControl  RouteAuthClass = "manager_control_auth"
	RouteAuthCPAManagement   RouteAuthClass = "cpa_management_key"
	RouteAuthDataPlane       RouteAuthClass = "data_plane_auth"
	RouteAuthRetiredDeny     RouteAuthClass = "retired_deny"
)

type CredentialAuthority string

const (
	CredentialNone           CredentialAuthority = "none"
	CredentialManagerSession CredentialAuthority = "manager_session"
	CredentialManager        CredentialAuthority = "manager_admin_or_session"
	CredentialCPA            CredentialAuthority = "cpa_management_key"
	CredentialDataPlane      CredentialAuthority = "data_plane_client"
	CredentialNeverForward   CredentialAuthority = "never_forward"
)

type RouteFallbackPolicy string

const (
	FallbackManagerReject RouteFallbackPolicy = "manager_reject"
	FallbackGateway       RouteFallbackPolicy = "gateway"
	FallbackNoGateway     RouteFallbackPolicy = "no_gateway_fallback"
	FallbackPanel         RouteFallbackPolicy = "panel_reject"
)

type RouteTerminalBehavior string

const (
	TerminalActive         RouteTerminalBehavior = "active"
	TerminalGeneric404     RouteTerminalBehavior = "generic_404"
	TerminalMethodPolicy   RouteTerminalBehavior = "manager_method_policy"
	TerminalDeny           RouteTerminalBehavior = "deny"
	TerminalGatewayForward RouteTerminalBehavior = "gateway_forward"
)

type RouteCollisionClass string

const (
	CollisionFixedTransport RouteCollisionClass = "fixed_transport"
	CollisionCanonical      RouteCollisionClass = "canonical_namespace"
	CollisionGateway        RouteCollisionClass = "gateway_protocol"
	CollisionNestedSpecific RouteCollisionClass = "nested_specificity"
	CollisionRetired        RouteCollisionClass = "retired_namespace"
)

type RouteOwnerEntry struct {
	ID                  string
	ContractVersion     string
	MethodClass         RouteMethodClass
	Path                string
	PathBinding         string
	Namespace           string
	Lifecycle           RouteLifecycle
	PublicOwner         RouteOwnerClass
	InternalOwner       RouteOwnerClass
	AuthClass           RouteAuthClass
	CredentialAuthority CredentialAuthority
	FallbackPolicy      RouteFallbackPolicy
	TerminalBehavior    RouteTerminalBehavior
	CollisionClass      RouteCollisionClass
}

const (
	RouteContractVersion        = "cpamp.phase4-01.route-owner.v1"
	PathBindingExact            = "exact"
	FixedBootstrapDiscoveryPath = "/.well-known/cpamp-bootstrap"
	FixedLivenessPath           = "/health"
	NamespaceFixedDiscovery     = "fixed-bootstrap-discovery"
	NamespacePublicAPIV2        = "<controlBasePath>/api/v2/*"
	NamespaceManagerControl     = "<controlBasePath>/control-plane"
	NamespaceFullWeb            = "<controlBasePath>/*"
	NamespacePanelLite          = "cpa-panel:/management.html"
	NamespaceGateway            = "outside-control-namespaces"
	NamespaceFixedLiveness      = "fixed-transport-liveness"
	NamespaceRetiredControl     = "retired-verified-control-namespace"
)

type RouteOwnerLedger struct {
	ContractVersion             string
	ProductionUpgradeEnabled    bool
	FixedBootstrapDiscoveryPath string
	Entries                     []RouteOwnerEntry
}

func validRouteMethodClass(value RouteMethodClass) bool {
	switch value {
	case RouteMethodAll, RouteMethodReadOnly, RouteMethodNamespace, RouteMethodLiveness, RouteMethodStatic:
		return true
	default:
		return false
	}
}

func validRouteLifecycle(value RouteLifecycle) bool {
	switch value {
	case RouteLifecycleActive, RouteLifecycleTerminal, RouteLifecycleRetired:
		return true
	default:
		return false
	}
}

func validOwner(value RouteOwnerClass) bool {
	switch value {
	case OwnerBootstrapDiscovery, OwnerPublicAPIV2, OwnerManagerControl, OwnerGatewayDataPlane,
		OwnerFullWebStatic, OwnerPanelLite, OwnerFixedTransport, OwnerRetiredControlDeny:
		return true
	default:
		return false
	}
}

func validRouteAuth(value RouteAuthClass) bool {
	switch value {
	case RouteAuthUnauthenticated, RouteAuthManagerSession, RouteAuthManagerControl,
		RouteAuthCPAManagement, RouteAuthDataPlane, RouteAuthRetiredDeny:
		return true
	default:
		return false
	}
}

func validCredentialAuthority(value CredentialAuthority) bool {
	switch value {
	case CredentialNone, CredentialManagerSession, CredentialManager, CredentialCPA,
		CredentialDataPlane, CredentialNeverForward:
		return true
	default:
		return false
	}
}

func validFallback(value RouteFallbackPolicy) bool {
	switch value {
	case FallbackManagerReject, FallbackGateway, FallbackNoGateway, FallbackPanel:
		return true
	default:
		return false
	}
}

func validTerminal(value RouteTerminalBehavior) bool {
	switch value {
	case TerminalActive, TerminalGeneric404, TerminalMethodPolicy, TerminalDeny, TerminalGatewayForward:
		return true
	default:
		return false
	}
}

func validCollision(value RouteCollisionClass) bool {
	switch value {
	case CollisionFixedTransport, CollisionCanonical, CollisionGateway, CollisionNestedSpecific, CollisionRetired:
		return true
	default:
		return false
	}
}

func (e RouteOwnerEntry) Validate() error {
	if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.Namespace) == "" || e.ContractVersion != RouteContractVersion ||
		!validRouteMethodClass(e.MethodClass) || !validRouteLifecycle(e.Lifecycle) ||
		!validOwner(e.PublicOwner) || !validOwner(e.InternalOwner) || !validRouteAuth(e.AuthClass) ||
		!validCredentialAuthority(e.CredentialAuthority) || !validFallback(e.FallbackPolicy) ||
		!validTerminal(e.TerminalBehavior) || !validCollision(e.CollisionClass) {
		return errors.New("route owner entry contains an unknown or missing contract value")
	}
	if e.Path == "" || strings.TrimSpace(e.Path) != e.Path || e.PathBinding != PathBindingExact {
		return errors.New("route owner entry must use an exact, non-empty path binding")
	}
	if e.PublicOwner != e.InternalOwner {
		return errors.New("public and internal route ownership conflict")
	}
	if e.PublicOwner == OwnerGatewayDataPlane {
		if e.FallbackPolicy != FallbackGateway || e.TerminalBehavior != TerminalGatewayForward {
			return errors.New("Gateway ownership requires Gateway fallback and terminal behavior")
		}
	} else if e.FallbackPolicy == FallbackGateway || e.TerminalBehavior == TerminalGatewayForward {
		return errors.New("Control/static/retired ownership cannot fall back to or forward to Gateway")
	}
	if e.PublicOwner == OwnerBootstrapDiscovery {
		if e.Path != FixedBootstrapDiscoveryPath || e.AuthClass != RouteAuthUnauthenticated ||
			e.CredentialAuthority != CredentialNone || e.FallbackPolicy == FallbackGateway ||
			e.TerminalBehavior != TerminalMethodPolicy {
			return errors.New("fixed discovery ownership is not fail-closed")
		}
	}
	if e.PublicOwner == OwnerRetiredControlDeny {
		if e.Lifecycle != RouteLifecycleRetired || e.FallbackPolicy == FallbackGateway ||
			e.AuthClass != RouteAuthRetiredDeny || e.CredentialAuthority != CredentialNeverForward || e.TerminalBehavior != TerminalDeny {
			return errors.New("retired Control ownership must deny without forwarding credentials")
		}
	}
	switch e.PublicOwner {
	case OwnerPublicAPIV2:
		if e.FallbackPolicy == FallbackGateway || e.AuthClass != RouteAuthManagerSession || e.CredentialAuthority != CredentialManagerSession {
			return errors.New("Public API v2 cannot fall back to Gateway and requires Manager session auth")
		}
	case OwnerManagerControl:
		if e.AuthClass != RouteAuthManagerControl || e.CredentialAuthority != CredentialManager {
			return errors.New("Manager Control requires Manager control auth and authority")
		}
	case OwnerGatewayDataPlane:
		if e.AuthClass != RouteAuthDataPlane || e.CredentialAuthority != CredentialDataPlane {
			return errors.New("Gateway ownership requires data-plane auth and authority")
		}
	case OwnerFullWebStatic, OwnerFixedTransport:
		if e.AuthClass != RouteAuthUnauthenticated || e.CredentialAuthority != CredentialNone {
			return errors.New("public static and liveness ownership cannot carry credentials")
		}
	case OwnerPanelLite:
		if e.FallbackPolicy != FallbackPanel || e.AuthClass != RouteAuthCPAManagement || e.CredentialAuthority != CredentialCPA {
			return errors.New("Panel Lite ownership requires panel-local rejection and CPA authority")
		}
	}
	if e.Lifecycle == RouteLifecycleTerminal && e.TerminalBehavior == TerminalActive {
		return errors.New("terminal route cannot have active terminal behavior")
	}
	return nil
}

func (l RouteOwnerLedger) Validate() error {
	if l.ContractVersion != RouteContractVersion || l.ProductionUpgradeEnabled ||
		l.FixedBootstrapDiscoveryPath != FixedBootstrapDiscoveryPath || len(l.Entries) == 0 {
		return errors.New("route owner ledger has an invalid contract header")
	}
	seen := map[string]bool{}
	routeKeys := map[string]string{}
	owners := map[RouteOwnerClass]bool{}
	for _, entry := range l.Entries {
		if seen[entry.ID] {
			return fmt.Errorf("duplicate route owner entry %q", entry.ID)
		}
		seen[entry.ID] = true
		owners[entry.PublicOwner] = true
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("%s: %w", entry.ID, err)
		}
		key := fmt.Sprintf("%s|%s|%s", entry.PathBinding, entry.Path, entry.MethodClass)
		if previous, exists := routeKeys[key]; exists {
			return fmt.Errorf("route ownership %q duplicates %q", entry.ID, previous)
		}
		routeKeys[key] = entry.ID
	}
	for _, owner := range []RouteOwnerClass{
		OwnerBootstrapDiscovery, OwnerPublicAPIV2, OwnerManagerControl,
		OwnerGatewayDataPlane, OwnerFullWebStatic, OwnerPanelLite,
		OwnerFixedTransport, OwnerRetiredControlDeny,
	} {
		if !owners[owner] {
			return fmt.Errorf("route owner ledger is missing %q", owner)
		}
	}
	return nil
}
