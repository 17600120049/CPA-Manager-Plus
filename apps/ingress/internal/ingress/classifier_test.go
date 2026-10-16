package ingress

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransitionalClassifier(t *testing.T) {
	t.Parallel()
	classifier := NewTransitionalClassifier()
	tests := []struct {
		path string
		want Route
	}{
		{path: "/", want: RouteManager},
		{path: "/management.html", want: RouteManager},
		{path: "/health", want: RouteManager},
		{path: "/status", want: RouteManager},
		{path: "/setup", want: RouteManager},
		{path: FixedBootstrapDiscoveryPath, want: RouteManager},
		{path: FixedBootstrapDiscoveryPath + "/extra", want: RouteManager},
		{path: "/usage-service", want: RouteManager},
		{path: "/usage-service/info", want: RouteManager},
		{path: "/v0/management", want: RouteManager},
		{path: "/v0/management/config", want: RouteManager},
		{path: "/v1/models", want: RouteGateway},
		{path: "/v1/chat/completions", want: RouteGateway},
		{path: "/v1/responses", want: RouteGateway},
		{path: "/v1/messages", want: RouteGateway},
		{path: "/future-gateway-protocol", want: RouteGateway},
		{path: "/usage-services", want: RouteGateway},
		{path: "/v0/management-other", want: RouteGateway},
	}
	for _, test := range tests {
		test := test
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()
			if got := classifier.Classify(test.path); got != test.want {
				t.Fatalf("Classify(%q) = %v, want %v", test.path, got, test.want)
			}
		})
	}
}

func TestClassifierRequestKeepsAmbiguousControlCandidatesWithManager(t *testing.T) {
	control := ControlMatcherFunc(func(value string) bool {
		return value == "/control" || value == "/control/" || len(value) > len("/control/") && strings.HasPrefix(value, "/control/")
	})
	retired := ControlMatcherFunc(func(value string) bool {
		return value == "/retired" || strings.HasPrefix(value, "/retired/")
	})
	classifier := NewOwnershipClassifier(control, retired)
	for _, target := range []string{
		"/control/%2e%2e/control",
		"/control/%252e%252e/control",
		"/control%2fsettings",
		"/control%252fsettings",
		"/control\\settings",
		"/control//settings",
		"/retired/settings",
		"/retired%2fsettings",
		FixedBootstrapDiscoveryPath + "/",
		"/.well-known%2Fcpamp-bootstrap",
		"/.well-known%252Fcpamp-bootstrap",
	} {
		t.Run(target, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "http://example.test"+target, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer manager-looking-secret")
			if got := classifier.ClassifyRequest(req); got != RouteManager {
				t.Fatalf("ClassifyRequest(%q) = %v, want Manager", target, got)
			}
		})
	}
}

func TestClassifierRequestPreservesNormalGatewayOwnership(t *testing.T) {
	classifier := NewOwnershipClassifier(
		ControlMatcherFunc(func(value string) bool { return value == "/control" || strings.HasPrefix(value, "/control/") }),
		ControlMatcherFunc(func(value string) bool { return value == "/retired" || strings.HasPrefix(value, "/retired/") }),
	)
	for _, target := range []string{"/v1/responses", "/v1/chat/completions", "/models", "/control-plane", "/retiredish/models"} {
		req := httptest.NewRequest(http.MethodPost, "http://example.test"+target, nil)
		req.Header.Set("Authorization", "Bearer provider-secret")
		if got := classifier.ClassifyRequest(req); got != RouteGateway {
			t.Fatalf("ClassifyRequest(%q) = %v, want Gateway", target, got)
		}
	}
}

func TestVerifiedEffectiveClassifierRequiresAuthorityAndKeepsRetiredDeny(t *testing.T) {
	classifier, err := NewVerifiedEffectiveClassifier(RoutingSnapshot{
		DesiredControlBasePath:           "/control-new",
		VerifiedEffectiveControlBasePath: "/control-new",
		PreviousVerifiedControlPaths:     []string{"/control-old"},
		Revision:                         3,
		State:                            SnapshotVerifiedEffective,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path string
		want Route
	}{
		{path: "/control-new/api/v2/models", want: RouteManager},
		{path: "/control-old/api/v2/models", want: RouteManager},
		{path: "/control-newer/api/v2/models", want: RouteGateway},
		{path: "/v1/responses", want: RouteGateway},
	} {
		if got := classifier.Classify(test.path); got != test.want {
			t.Fatalf("Classify(%q) = %v, want %v", test.path, got, test.want)
		}
	}

	inactive, err := NewVerifiedEffectiveClassifier(RoutingSnapshot{
		DesiredControlBasePath: "/desired",
		Revision:               4,
		State:                  SnapshotDesiredOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := inactive.Classify("/desired/api/v2/models"); got != RouteManager {
		t.Fatalf("desired-only candidate classified as %v, want Manager deny ownership", got)
	}
}

func TestVerifiedEffectiveClassifierRejectsCollisionsAndUnsafePaths(t *testing.T) {
	for _, snapshot := range []RoutingSnapshot{
		{VerifiedEffectiveControlBasePath: "/v1", Revision: 1, State: SnapshotVerifiedEffective},
		{VerifiedEffectiveControlBasePath: FixedBootstrapDiscoveryPath, Revision: 1, State: SnapshotVerifiedEffective},
		{VerifiedEffectiveControlBasePath: "/control//child", Revision: 1, State: SnapshotVerifiedEffective},
		{VerifiedEffectiveControlBasePath: "/control%2fchild", Revision: 1, State: SnapshotVerifiedEffective},
		{DesiredControlBasePath: "/desired", VerifiedEffectiveControlBasePath: "/effective", State: SnapshotVerifiedEffective},
	} {
		if _, err := NewVerifiedEffectiveClassifier(snapshot); err == nil {
			t.Fatalf("unsafe snapshot accepted: %+v", snapshot)
		}
	}
}

func TestClassifierControlMatcherIsReplaceable(t *testing.T) {
	t.Parallel()
	classifier := NewClassifier(ControlMatcherFunc(func(path string) bool {
		return path == "/future-control" || len(path) > len("/future-control/") && path[:len("/future-control/")] == "/future-control/"
	}))
	if got := classifier.Classify("/future-control/settings"); got != RouteManager {
		t.Fatalf("future control path classified as %v", got)
	}
	if got := classifier.Classify("/health"); got != RouteGateway {
		t.Fatalf("transitional route leaked into replacement matcher: %v", got)
	}
}
