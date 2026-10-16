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

func TestClassifierRequestKeepsAmbiguousActiveCandidatesWithManager(t *testing.T) {
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

func TestClassifierRequestDeniesRetiredCandidates(t *testing.T) {
	classifier := NewOwnershipClassifier(
		ControlMatcherFunc(func(value string) bool { return value == "/control" || strings.HasPrefix(value, "/control/") }),
		ControlMatcherFunc(func(value string) bool { return value == "/retired" || strings.HasPrefix(value, "/retired/") }),
	)
	for _, target := range []string{"/retired/settings", "/retired%2fsettings", "/retired%252fsettings"} {
		t.Run(target, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "http://example.test"+target, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer manager-looking-secret")
			if got := classifier.ClassifyRequest(req); got != RouteDeny {
				t.Fatalf("ClassifyRequest(%q) = %v, want local deny", target, got)
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
