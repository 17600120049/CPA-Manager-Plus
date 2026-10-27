package proxy

import (
	"net/http"
	"strings"
	"testing"
)

func TestPluginResourceFetchMetadataIsFailClosed(t *testing.T) {
	header := func(values ...string) http.Header {
		h := http.Header{}
		for index := 0; index+1 < len(values); index += 2 {
			h.Set(values[index], values[index+1])
		}
		return h
	}
	cases := []struct {
		name   string
		header http.Header
		want   bool
	}{
		// A Control document framing, navigating to, or drawing a plugin resource
		// never executes plugin code with admin authority, so these are admitted.
		{"same-origin iframe", header("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "iframe"), true},
		{"same-origin document", header("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "document"), true},
		{"same-origin image", header("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "image"), true},
		// The exact bug this policy exists to stop: the admin document pulling a
		// plugin path as an executing subresource.
		{"same-origin script", header("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "script"), false},
		{"same-origin worker", header("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "worker"), false},
		{"same-origin sharedworker", header("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "sharedworker"), false},
		{"same-origin serviceworker", header("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "serviceworker"), false},
		{"same-origin style", header("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "style"), false},
		{"same-origin font", header("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "font"), false},
		{"same-origin object", header("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "object"), false},
		{"same-origin embed", header("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "embed"), false},
		{"same-origin fetch", header("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Dest", "empty"), false},
		{"same-origin missing dest", header("Sec-Fetch-Site", "same-origin"), false},
		// A sandboxed plugin document is an opaque origin, so the browser marks its
		// own subresource requests cross-site and the sandbox CSP keeps it opaque.
		{"cross-site plugin asset", header("Sec-Fetch-Site", "cross-site", "Sec-Fetch-Dest", "script"), true},
		{"same-site plugin asset", header("Sec-Fetch-Site", "same-site", "Sec-Fetch-Dest", "image"), true},
		{"user navigation", header("Sec-Fetch-Site", "none", "Sec-Fetch-Dest", "document"), true},
		// Fail closed: a client that cannot prove a browser sandbox context never
		// receives plugin content, so the private profile cannot be reached by a
		// proxy or an old browser that omits the header.
		{"no fetch metadata", http.Header{}, false},
		{"empty site", header("Sec-Fetch-Site", "", "Sec-Fetch-Dest", "iframe"), false},
		{"whitespace site", header("Sec-Fetch-Site", "   ", "Sec-Fetch-Dest", "iframe"), false},
		{"unknown site", header("Sec-Fetch-Site", "bogus", "Sec-Fetch-Dest", "iframe"), true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := pluginResourceFetchMetadataAllowed(testCase.header); got != testCase.want {
				t.Fatalf("allowed = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestPluginResourceSandboxHeadersOverridePluginResponse(t *testing.T) {
	header := http.Header{}
	// A plugin tries to grant itself same-origin authority, plant a credential and
	// opt out of sniffing. Every one of these must be replaced or removed.
	header.Set("Content-Security-Policy", "sandbox allow-scripts allow-same-origin allow-top-navigation")
	header.Set("Set-Cookie", "cpamp_admin=stolen; Path=/")
	header.Add("Set-Cookie", "second=also-stolen; Path=/")
	header.Set("X-Content-Type-Options", "sniff")
	header.Set("Access-Control-Allow-Origin", "https://evil.example")

	applyPluginResourceSandboxHeaders(header)

	if got := header.Get("Content-Security-Policy"); got != pluginResourceSandboxCSP {
		t.Fatalf("policy = %q, want %q", got, pluginResourceSandboxCSP)
	}
	if strings.Contains(header.Get("Content-Security-Policy"), "allow-same-origin") {
		t.Fatal("sandbox policy retained allow-same-origin")
	}
	if cookies := header.Values("Set-Cookie"); len(cookies) != 0 {
		t.Fatalf("Set-Cookie survived isolation: %v", cookies)
	}
	if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("nosniff = %q", got)
	}
	if got := header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("allow-origin = %q, want *", got)
	}
}

func TestPluginResourceRedirectConfinedToOwnPluginPrefix(t *testing.T) {
	const prefix = "/v0/resource/plugins/demo"
	cases := []struct {
		name     string
		location string
		want     bool
	}{
		{"own asset", "/v0/resource/plugins/demo/index.html", true},
		{"own nested asset", "/v0/resource/plugins/demo/assets/app.js", true},
		{"own prefix root", "/v0/resource/plugins/demo", true},
		{"relative escape to plugins root", "../../other/index.html", false},
		{"relative within plugin", "other.js", true},
		{"relative from nested asset", "sibling.js", true},
		// A redirect is the cheapest way out of the sandbox: into the Console, into
		// another plugin, or off-origin entirely.
		{"admin document", "/console/", false},
		{"other plugin", "/v0/resource/plugins/other/index.html", false},
		{"absolute same origin", "https://cpamp.example/v0/resource/plugins/demo/x.js", false},
		{"protocol relative", "//evil.example/x.js", false},
		{"dot segment escape", "/v0/resource/plugins/demo/../../console", false},
		{"backslash escape", `/v0/resource/plugins/demo\..\console`, false},
		{"empty", "", false},
		{"scheme only", "javascript:alert(1)", false},
		{"data url", "data:text/html,<script>alert(1)</script>", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := pluginResourceRedirectAllowed(testCase.location, "/v0/resource/plugins/demo/pages/view.html", prefix); got != testCase.want {
				t.Fatalf("allowed(%q) = %v, want %v", testCase.location, got, testCase.want)
			}
		})
	}
}

func TestPluginResourceRequestPrefixIsPerPlugin(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/v0/resource/plugins/demo/index.html", "/v0/resource/plugins/demo"},
		{"/v0/resource/plugins/demo", "/v0/resource/plugins/demo"},
		{"/v0/resource/plugins/demo/", "/v0/resource/plugins/demo"},
		{"/v0/resource/plugins/", ""},
		{"/v0/resource/plugins", ""},
		{"/v0/management/plugins", ""},
	}
	for _, testCase := range cases {
		if got := pluginResourceRequestPrefix(testCase.path); got != testCase.want {
			t.Fatalf("prefix(%q) = %q, want %q", testCase.path, got, testCase.want)
		}
	}
}
