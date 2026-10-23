package main

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

func guardFreshLegacyWrites(next http.Handler, wiringErr error) http.Handler {
	if wiringErr == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Console writes are unavailable", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

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
	if strings.HasPrefix(resource, "cpamp/") && legacyBridgePathAllowed(strings.TrimPrefix(resource, "cpamp/"), r.Method) {
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
	case resource == "cpamp/config" && (r.Method == http.MethodGet || r.Method == http.MethodPut):
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

// legacyBridgePathAllowed is an endpoint-and-method allowlist. The
// canonical API has already authenticated the request, but a generic path
// tunnel would turn the Full Web namespace into an unbounded Manager router.
// These families are the existing CPAMP contracts consumed by MainLayout and
// its real pages; unknown paths remain a fail-closed 404.
func legacyBridgePathAllowed(resource, method string) bool {
	if resource == "" || strings.ContainsAny(resource, "\\#?%") || strings.Contains(resource, "..") {
		return false
	}
	allowed := func(methods ...string) bool {
		return slices.Contains(methods, method)
	}
	switch resource {
	case "usage-service/info":
		return allowed(http.MethodGet)
	case "usage-service/config":
		return allowed(http.MethodGet, http.MethodPut)
	case "usage-service/account-processing-policy":
		return allowed(http.MethodGet, http.MethodPatch)
	case "usage-service/quota-cooldowns":
		return allowed(http.MethodGet)
	case "usage-service/quota-cooldowns/recover":
		return allowed(http.MethodPost)
	case "usage-service/updates":
		return allowed(http.MethodGet)
	case "usage-service/updates/check", "usage-service/updates/notification", "usage-service/updates/dismiss":
		return allowed(http.MethodPost)
	case "usage-service/updates/channel":
		return allowed(http.MethodPut)
	case "usage-service/runtime/updates":
		return allowed(http.MethodGet)
	case "usage-service/runtime/updates/check":
		return allowed(http.MethodPost)
	case "usage-service/runtime/updates/prepare", "usage-service/runtime/updates/activate":
		return allowed(http.MethodPost)
	case "usage-service/runtime/updates/operations/prepare", "usage-service/runtime/updates/operations/activate":
		return allowed(http.MethodGet)
	case "v0/management/logs":
		return allowed(http.MethodGet, http.MethodDelete)
	case "v0/management/request-error-logs":
		return allowed(http.MethodGet)
	case "v0/management/codex-auth-url", "v0/management/anthropic-auth-url", "v0/management/antigravity-auth-url", "v0/management/kimi-auth-url", "v0/management/xai-auth-url", "v0/management/get-auth-status":
		return allowed(http.MethodGet)
	case "v0/management/oauth-callback":
		return allowed(http.MethodPost)
	case "v0/management/latest-version":
		return allowed(http.MethodGet)
	case "v0/management/config.yaml":
		return allowed(http.MethodGet, http.MethodPut)
	case "v0/management/api-keys":
		return allowed(http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete)
	case "v0/management/api-key-aliases":
		return allowed(http.MethodGet, http.MethodPut)
	case "v0/management/codex-api-key", "v0/management/claude-api-key", "v0/management/gemini-api-key", "v0/management/interactions-api-key", "v0/management/vertex-api-key", "v0/management/xai-api-key", "v0/management/openai-compatibility":
		return allowed(http.MethodGet, http.MethodPut, http.MethodDelete)
	case "v0/management/config":
		return allowed(http.MethodGet, http.MethodPut)
	case "v0/management/debug", "v0/management/request-retry", "v0/management/quota-exceeded/switch-project", "v0/management/quota-exceeded/switch-preview-model", "v0/management/request-log", "v0/management/logging-to-file", "v0/management/ws-auth":
		return allowed(http.MethodPut)
	case "v0/management/proxy-url":
		return allowed(http.MethodPut, http.MethodDelete)
	case "v0/management/logs-max-total-size-mb", "v0/management/force-model-prefix", "v0/management/routing/strategy":
		return allowed(http.MethodGet, http.MethodPut)
	case "v0/management/api-key-usage":
		return allowed(http.MethodGet)
	case "v0/management/api-call", "v0/management/vertex/import":
		return allowed(http.MethodPost)
	case "v0/management/dashboard/summary", "v0/management/usage", "v0/management/usage/export", "v0/management/model-prices/usage-summary":
		return allowed(http.MethodGet)
	case "v0/management/usage/import", "v0/management/usage/import-sessions":
		return allowed(http.MethodPost)
	case "v0/management/model-prices":
		return allowed(http.MethodGet, http.MethodPut)
	case "v0/management/model-prices/sync":
		return allowed(http.MethodPost)
	case "v0/management/account-action-candidates":
		return allowed(http.MethodGet)
	case "v0/management/codex-inspection/runs":
		return allowed(http.MethodGet)
	case "v0/management/monitoring/header-snapshots":
		return allowed(http.MethodGet)
	case "v0/management/monitoring/account-history", "v0/management/monitoring/account-window-usage", "v0/management/monitoring/analytics":
		return allowed(http.MethodPost)
	case "v0/management/quota-snapshots", "v0/management/quota-snapshots/query":
		return allowed(http.MethodPost)
	case "v0/management/auth-files":
		return allowed(http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete)
	case "v0/management/auth-files/status":
		return allowed(http.MethodGet, http.MethodPatch)
	case "v0/management/auth-files/fields":
		return allowed(http.MethodGet, http.MethodPatch)
	case "v0/management/auth-files/download", "v0/management/auth-files/models":
		return allowed(http.MethodGet)
	case "v0/management/oauth-excluded-models":
		return allowed(http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete)
	case "v0/management/oauth-model-alias":
		return allowed(http.MethodGet, http.MethodPatch, http.MethodDelete)
	case "v0/management/reset-quota":
		return allowed(http.MethodPost)
	case "status", "health", "v1/models", "models":
		return allowed(http.MethodGet)
	default:
		return legacyBridgeDynamicPathAllowed(resource, method, allowed)
	}
}

func legacyBridgeDynamicPathAllowed(resource, method string, allowed func(...string) bool) bool {
	const management = "v0/management/"
	if !strings.HasPrefix(resource, management) {
		return false
	}
	path := strings.TrimPrefix(resource, management)
	parts := strings.Split(path, "/")
	if len(parts) < 2 || !safeBridgePathSegment(parts[len(parts)-1]) {
		return false
	}
	// Request logs and API-key aliases use opaque single path segments.
	if len(parts) == 2 && parts[0] == "request-error-logs" {
		return allowed(http.MethodGet)
	}
	if len(parts) == 2 && parts[0] == "request-log-by-id" {
		return allowed(http.MethodGet)
	}
	if len(parts) == 2 && parts[0] == "api-key-aliases" {
		return allowed(http.MethodDelete)
	}
	if len(parts) == 2 && parts[0] == "model-definitions" {
		return allowed(http.MethodGet)
	}
	if len(parts) == 2 && parts[0] == "codex-inspection" && parts[1] == "run" {
		return allowed(http.MethodPost)
	}
	if len(parts) == 2 && parts[0] == "usage" && parts[1] == "import-sessions" {
		return allowed(http.MethodPost)
	}
	if len(parts) == 3 && parts[0] == "codex-inspection" && parts[1] == "runs" && safeBridgeNumericSegment(parts[2]) {
		return allowed(http.MethodGet)
	}
	if len(parts) == 4 && parts[0] == "codex-inspection" && parts[1] == "runs" && safeBridgeNumericSegment(parts[2]) {
		switch parts[3] {
		case "cancel", "actions":
			return allowed(http.MethodPost)
		}
	}
	if len(parts) == 3 && parts[0] == "account-action-candidates" && safeBridgeNumericSegment(parts[1]) {
		switch parts[2] {
		case "ignore", "resolve", "enable":
			return allowed(http.MethodPost)
		case "auth-file":
			return allowed(http.MethodDelete)
		}
	}
	if len(parts) == 3 && parts[0] == "usage" && parts[1] == "import-sessions" && safeBridgePathSegment(parts[2]) {
		return allowed(http.MethodGet, http.MethodDelete)
	}
	if len(parts) == 4 && parts[0] == "usage" && parts[1] == "import-sessions" && safeBridgePathSegment(parts[2]) {
		switch parts[3] {
		case "chunk":
			return allowed(http.MethodPut)
		case "complete":
			return allowed(http.MethodPost)
		}
	}
	return false
}

func safeBridgePathSegment(value string) bool {
	if value == "" || strings.ContainsAny(value, "/\\#?%") {
		return false
	}
	for _, b := range []byte(value) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == '.' {
			continue
		}
		return false
	}
	return value != "." && value != ".."
}

func safeBridgeNumericSegment(value string) bool {
	if value == "" {
		return false
	}
	for _, b := range []byte(value) {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}
