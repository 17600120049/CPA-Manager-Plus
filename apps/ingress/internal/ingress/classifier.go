package ingress

import (
	"net/http"
	"net/url"
	"path"
	"strings"
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
	RouteDeny
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

func NewClassifier(control ControlMatcher) Classifier {
	if control == nil {
		panic("ingress: control matcher is required")
	}
	return Classifier{control: control}
}

// NewOwnershipClassifier adds a deny-only matcher for previous verified
// Control namespaces. Retired paths are rejected in Ingress, before any
// Manager or Gateway proxy can receive Manager/Admin credentials.
func NewOwnershipClassifier(control ControlMatcher, retired ControlMatcher) Classifier {
	if control == nil {
		panic("ingress: control matcher is required")
	}
	return Classifier{control: control, retired: retired}
}

func (c Classifier) Classify(path string) Route {
	for _, candidate := range routeCandidates(path) {
		if fixedDiscoveryCandidate(candidate) || c.control.Matches(candidate) {
			return RouteManager
		}
		if c.retired != nil && c.retired.Matches(candidate) {
			return RouteDeny
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
			if fixedDiscoveryCandidate(candidate) || c.control.Matches(candidate) {
				return RouteManager
			}
			if c.retired != nil && c.retired.Matches(candidate) {
				return RouteDeny
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
