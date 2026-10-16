package ingress

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode"
)

// FixedBootstrapDiscoveryPath is the only fixed unauthenticated Bootstrap
// carrier. It is intentionally duplicated at the module boundary because the
// Ingress module must remain stdlib-only; repo contract tests assert the value
// against the Manager routing contract and fixture.
const FixedBootstrapDiscoveryPath = "/.well-known/cpamp-bootstrap"

// Route identifies the internal owner of a public request.
type Route uint8

const (
	RouteGateway Route = iota
	RouteManager
)

// ControlMatcher is the single replacement seam for control route ownership.
// Runtime 12 supplies transitional path evidence. A later configuration slice
// can replace it with an effective controlBasePath matcher without changing the
// transport proxies or Compose topology.
type ControlMatcher interface {
	Matches(path string) bool
}

type ControlMatcherFunc func(path string) bool

func (f ControlMatcherFunc) Matches(path string) bool {
	return f(path)
}

type Classifier struct {
	control ControlMatcher
	retired ControlMatcher
}

// RoutingSnapshot is the Ingress-side transport projection of Manager's
// authoritative snapshot. It is supplied by a later read-only consumer; this
// module never discovers or persists any of these values.
type RoutingSnapshot struct {
	DesiredControlBasePath           string
	VerifiedEffectiveControlBasePath string
	PreviousVerifiedControlPaths     []string
	Revision                         uint64
	State                            string
}

const (
	SnapshotVerifiedEffective = "verified_effective"
	SnapshotDesiredOnly       = "desired_only"
	SnapshotStale             = "stale"
	SnapshotUnknown           = "unknown"
	SnapshotUnavailable       = "unavailable"
)

func NewClassifier(control ControlMatcher) Classifier {
	if control == nil {
		panic("ingress: control matcher is required")
	}
	return Classifier{control: control}
}

// NewOwnershipClassifier adds a deny-only matcher for previous verified
// Control namespaces. Retired paths remain Manager-owned so a later Manager
// handler can reject them before any Gateway request is attempted.
func NewOwnershipClassifier(control ControlMatcher, retired ControlMatcher) Classifier {
	if control == nil {
		panic("ingress: control matcher is required")
	}
	return Classifier{control: control, retired: retired}
}

// NewVerifiedEffectiveClassifier consumes only a verified-effective routing
// snapshot. Desired, stale, unknown, and unavailable snapshots leave the
// public Control matcher inactive while preserving bounded retired deny paths.
func NewVerifiedEffectiveClassifier(snapshot RoutingSnapshot) (Classifier, error) {
	if err := validateRoutingSnapshot(snapshot); err != nil {
		return Classifier{}, err
	}
	controlPath := ""
	if snapshot.State == SnapshotVerifiedEffective {
		controlPath = snapshot.VerifiedEffectiveControlBasePath
	}
	control := ControlMatcherFunc(func(value string) bool {
		return controlPath != "" && segmentPath(value, controlPath)
	})
	retiredPaths := append([]string(nil), snapshot.PreviousVerifiedControlPaths...)
	appendPath := func(value string) {
		if value != "" {
			retiredPaths = append(retiredPaths, value)
		}
	}
	if snapshot.State != SnapshotVerifiedEffective {
		appendPath(snapshot.VerifiedEffectiveControlBasePath)
	}
	if snapshot.DesiredControlBasePath != snapshot.VerifiedEffectiveControlBasePath {
		appendPath(snapshot.DesiredControlBasePath)
	}
	retired := ControlMatcherFunc(func(value string) bool {
		for _, prefix := range retiredPaths {
			if segmentPath(value, prefix) {
				return true
			}
		}
		return false
	})
	return NewOwnershipClassifier(control, retired), nil
}

func (c Classifier) Classify(path string) Route {
	for _, candidate := range routeCandidates(path) {
		if fixedDiscoveryCandidate(candidate) || c.control.Matches(candidate) || c.retired != nil && c.retired.Matches(candidate) {
			return RouteManager
		}
	}
	return RouteGateway
}

// ClassifyRequest inspects URL.Path, RawPath/EscapedPath, and RequestURI
// candidates before ownership is selected. Up to two unescapes are considered
// so encoded and double-encoded separators/dot segments cannot turn a
// Control-owned request into Gateway traffic after an intermediary normalizes
// it. Credential headers are deliberately not read here.
func (c Classifier) ClassifyRequest(r *http.Request) Route {
	if r == nil || r.URL == nil {
		return RouteGateway
	}
	pathValue := r.URL.Path
	if pathValue == "" {
		pathValue = "/"
	}
	values := []string{pathValue, r.URL.RawPath, r.URL.EscapedPath(), r.RequestURI}
	for _, value := range values {
		if value == "" {
			continue
		}
		if index := strings.IndexByte(value, '?'); index >= 0 {
			value = value[:index]
		}
		for _, candidate := range routeCandidates(value) {
			if fixedDiscoveryCandidate(candidate) || c.control.Matches(candidate) || c.retired != nil && c.retired.Matches(candidate) {
				return RouteManager
			}
		}
	}
	return RouteGateway
}

func routeCandidates(value string) []string {
	if value == "" {
		value = "/"
	}
	seen := make(map[string]struct{}, 10)
	result := make([]string, 0, 10)
	add := func(candidate string) {
		if candidate == "" {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		result = append(result, candidate)
		normalized := path.Clean(strings.ReplaceAll(candidate, "\\", "/"))
		if normalized != candidate {
			if _, ok := seen[normalized]; !ok {
				seen[normalized] = struct{}{}
				result = append(result, normalized)
			}
		}
	}
	current := value
	for pass := 0; pass < 3; pass++ {
		add(current)
		decoded, err := url.PathUnescape(current)
		if err != nil || decoded == current {
			break
		}
		current = decoded
	}
	return result
}

func fixedDiscoveryCandidate(value string) bool {
	if value == FixedBootstrapDiscoveryPath || strings.HasPrefix(value, FixedBootstrapDiscoveryPath+"/") {
		return true
	}
	normalized := path.Clean(strings.ReplaceAll(value, "\\", "/"))
	return normalized == FixedBootstrapDiscoveryPath || strings.HasPrefix(normalized, FixedBootstrapDiscoveryPath+"/")
}

func segmentPath(value, prefix string) bool {
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}

func validateRoutingSnapshot(snapshot RoutingSnapshot) error {
	validStates := map[string]bool{
		SnapshotVerifiedEffective: true,
		SnapshotDesiredOnly:       true,
		SnapshotStale:             true,
		SnapshotUnknown:           true,
		SnapshotUnavailable:       true,
	}
	if !validStates[snapshot.State] {
		return fmt.Errorf("unknown routing snapshot state %q", snapshot.State)
	}
	if snapshot.DesiredControlBasePath != "" && !validControlBasePath(snapshot.DesiredControlBasePath) {
		return fmt.Errorf("desired controlBasePath is not canonical: %q", snapshot.DesiredControlBasePath)
	}
	if snapshot.VerifiedEffectiveControlBasePath != "" && !validControlBasePath(snapshot.VerifiedEffectiveControlBasePath) {
		return fmt.Errorf("verified effective controlBasePath is not canonical: %q", snapshot.VerifiedEffectiveControlBasePath)
	}
	if snapshot.State == SnapshotVerifiedEffective && (snapshot.VerifiedEffectiveControlBasePath == "" || snapshot.Revision == 0) {
		return fmt.Errorf("verified effective snapshot requires path and revision")
	}
	if snapshot.State == SnapshotDesiredOnly && (snapshot.DesiredControlBasePath == "" || snapshot.VerifiedEffectiveControlBasePath != "") {
		return fmt.Errorf("desired-only snapshot requires desired path and no effective path")
	}
	if snapshot.State == SnapshotStale && (snapshot.VerifiedEffectiveControlBasePath == "" || snapshot.Revision == 0) {
		return fmt.Errorf("stale snapshot requires last effective path and revision")
	}
	if (snapshot.State == SnapshotUnknown || snapshot.State == SnapshotUnavailable) && snapshot.VerifiedEffectiveControlBasePath != "" {
		return fmt.Errorf("unknown or unavailable snapshot cannot carry effective path")
	}
	paths := make([]string, 0, len(snapshot.PreviousVerifiedControlPaths)+2)
	reserved := []string{
		FixedBootstrapDiscoveryPath,
		"/health",
		"/management.html",
		"/models",
		"/setup",
		"/status",
		"/v0",
		"/v1",
		"/usage-service",
	}
	for _, candidate := range []string{snapshot.DesiredControlBasePath, snapshot.VerifiedEffectiveControlBasePath} {
		if candidate != "" {
			for _, fixed := range reserved {
				if candidate == fixed || segmentPath(candidate, fixed) || segmentPath(fixed, candidate) {
					return fmt.Errorf("controlBasePath collides with reserved path: %q", candidate)
				}
			}
			paths = append(paths, candidate)
		}
	}
	for _, retired := range snapshot.PreviousVerifiedControlPaths {
		if !validControlBasePath(retired) {
			return fmt.Errorf("retired controlBasePath is not canonical: %q", retired)
		}
		for _, fixed := range reserved {
			if retired == fixed || segmentPath(fixed, retired) || segmentPath(retired, fixed) {
				return fmt.Errorf("retired controlBasePath collides with reserved path: %q", retired)
			}
		}
		for _, existing := range paths {
			if existing == retired || segmentPath(existing, retired) || segmentPath(retired, existing) {
				return fmt.Errorf("routing namespace collision: %q and %q", existing, retired)
			}
		}
		paths = append(paths, retired)
	}
	return nil
}

func validControlBasePath(value string) bool {
	if value == "" || value == "/" || !strings.HasPrefix(value, "/") || strings.TrimSpace(value) != value ||
		strings.Contains(value, "//") || strings.ContainsAny(value, "?#\\%") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return path.Clean(value) == value
}

// NewTransitionalClassifier reflects only the Manager paths proven by the
// current v2 router. These paths are deployment compatibility evidence, not
// the long-term CPAMP public contract.
func NewTransitionalClassifier() Classifier {
	exact := map[string]struct{}{
		"/":                         {},
		"/health":                   {},
		"/management.html":          {},
		"/setup":                    {},
		"/status":                   {},
		FixedBootstrapDiscoveryPath: {},
	}
	prefixes := []string{
		"/usage-service",
		"/v0/management",
	}
	return NewClassifier(ControlMatcherFunc(func(path string) bool {
		if _, ok := exact[path]; ok {
			return true
		}
		for _, prefix := range prefixes {
			if path == prefix || len(path) > len(prefix) && path[:len(prefix)] == prefix && path[len(prefix)] == '/' {
				return true
			}
		}
		return false
	}))
}
