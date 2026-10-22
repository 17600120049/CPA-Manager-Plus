package main

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// legacyResource exposes the existing CPAMP service/controller contracts below
// the canonical Full Web API namespace. The browser never learns or constructs
// the legacy paths; this adapter keeps the Public API authority and admin
// verification at the Fresh boundary while reusing the original business
// services and response shapes.
func (a *freshApplication) legacyResource(w http.ResponseWriter, r *http.Request, resource string) {
	if a.legacy == nil {
		http.Error(w, "resource unavailable", http.StatusServiceUnavailable)
		return
	}
	// Full Web uses this bridge only after canonicalAPI has verified the
	// Manager Admin key. Keep the browser-facing namespace explicit while
	// reusing the complete legacy router and its own controller checks.
	if strings.HasPrefix(resource, "cpamp/") && legacyBridgePathAllowed(strings.TrimPrefix(resource, "cpamp/")) {
		targetResource := strings.TrimPrefix(resource, "cpamp/")
		parsed, err := url.Parse("/" + targetResource)
		if err != nil || parsed.Path == "" || parsed.Path == "/" {
			http.NotFound(w, r)
			return
		}
		parsed.RawQuery = r.URL.RawQuery
		clone := r.Clone(r.Context())
		clone.URL = parsed
		clone.RequestURI = parsed.RequestURI()
		a.legacy.ServeHTTP(w, clone)
		return
	}
	var target string
	switch {
	case resource == "cpamp/dashboard" && r.Method == http.MethodGet:
		// The legacy dashboard contract requires a day boundary. Keep this
		// server-owned so normal Console navigation has no internal query step.
		today := time.Now()
		start := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location()).UnixMilli()
		target = "/v0/management/dashboard/summary?today_start_ms=" + strconv.FormatInt(start, 10)
	case resource == "cpamp/config":
		target = "/usage-service/config"
	case resource == "cpamp/status" && r.Method == http.MethodGet:
		target = "/status"
	case resource == "cpamp/usage" && r.Method == http.MethodGet:
		target = "/v0/management/usage"
	case resource == "cpamp/model-prices" && (r.Method == http.MethodGet || r.Method == http.MethodPut):
		target = "/v0/management/model-prices"
	case resource == "cpamp/monitoring" && r.Method == http.MethodPost:
		target = "/v0/management/monitoring/analytics"
	default:
		http.NotFound(w, r)
		return
	}
	parsed, err := url.Parse(target)
	if err != nil {
		http.Error(w, "resource unavailable", http.StatusServiceUnavailable)
		return
	}
	clone := r.Clone(r.Context())
	clone.URL = parsed
	clone.RequestURI = parsed.RequestURI()
	a.legacy.ServeHTTP(w, clone)
}

// legacyBridgePathAllowed is deliberately a route-family allowlist. The
// canonical API has already authenticated the request, but a generic path
// tunnel would turn the Full Web namespace into an unbounded Manager router.
// These families are the existing CPAMP contracts consumed by MainLayout and
// its real pages; unknown paths remain a fail-closed 404.
func legacyBridgePathAllowed(resource string) bool {
	if resource == "" || strings.ContainsAny(resource, "\\#") || strings.Contains(resource, "..") {
		return false
	}
	for _, prefix := range []string{
		"v0/management/",
		"usage-service/",
	} {
		if strings.HasPrefix(resource, prefix) {
			return true
		}
	}
	switch resource {
	case "status", "health", "setup", "v1/models", "models":
		return true
	default:
		return false
	}
}
