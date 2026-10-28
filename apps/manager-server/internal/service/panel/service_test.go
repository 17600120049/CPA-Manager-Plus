package panel

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

const embeddedPanelFile = "web/management.html"

const embeddedPanelBody = "<html><body>embedded panel</body></html>"

// panelWithScripts is the shape the real panel has: inline scripts that a nonce
// policy must admit.
const panelWithScripts = "<html><body><script>a()</script><script type=\"module\">b()</script></body></html>"

func newEmbeddedService(t *testing.T, body string) *Service {
	t.Helper()
	return New("", fstest.MapFS{embeddedPanelFile: &fstest.MapFile{Data: []byte(body)}})
}

func serve(t *testing.T, s *Service, method, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	for key, values := range header {
		for _, value := range values {
			r.Header.Add(key, value)
		}
	}
	rr := httptest.NewRecorder()
	s.ServeManagementHTML(rr, r, func(w http.ResponseWriter, status int, err error) {
		http.Error(w, err.Error(), status)
	})
	return rr
}

func TestEmbeddedPanelIsServedNoStoreWithoutValidators(t *testing.T) {
	s := newEmbeddedService(t, embeddedPanelBody)

	rr := serve(t, s, http.MethodGet, "/management.html", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if rr.Body.String() != embeddedPanelBody {
		t.Fatalf("body = %q, want %q", rr.Body.String(), embeddedPanelBody)
	}
	if got, want := rr.Header().Get("Content-Length"), strconv.Itoa(len(embeddedPanelBody)); got != want {
		t.Fatalf("content length = %q, want %q", got, want)
	}
	if !strings.Contains(rr.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("content type = %q", rr.Header().Get("Content-Type"))
	}
	// no-store, not no-cache: the body is only valid with the nonce its own
	// response carried, so it must not be reused from any cache.
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q, want no-store", got)
	}
	// No validator may exist, because a 304 would pair a fresh nonce header with
	// the stored body's old nonce.
	if got := rr.Header().Get("ETag"); got != "" {
		t.Fatalf("etag = %q, want none", got)
	}
	if got := rr.Header().Get("Last-Modified"); got != "" {
		t.Fatalf("last modified = %q, want none", got)
	}
}

// A conditional request must be answered with a full response whose nonce
// matches its own body. Answering 304 would leave the browser's stored body
// carrying the previous nonce while the policy named the new one, and every
// script element in the admin page would be blocked.
func TestEmbeddedPanelAnswersConditionalRequestWithMatchingNonce(t *testing.T) {
	// A document with scripts, so the response carries a nonce policy at all.
	s := newEmbeddedService(t, panelWithScripts)
	first := serve(t, s, http.MethodGet, "/management.html", nil)
	firstPolicy := first.Header().Get("Content-Security-Policy")
	if !strings.HasPrefix(firstPolicy, "script-src 'nonce-") {
		t.Fatalf("policy = %q, want a nonce policy for a document with scripts", firstPolicy)
	}

	for _, header := range []http.Header{
		{"If-None-Match": {`"anything"`}},
		{"If-None-Match": {"*"}},
		{"If-Modified-Since": {"Wed, 21 Oct 2026 07:28:00 GMT"}},
	} {
		rr := serve(t, s, http.MethodGet, "/management.html", header)
		if rr.Code != http.StatusOK {
			t.Fatalf("%v status = %d, want 200", header, rr.Code)
		}
		if rr.Body.Len() == 0 {
			t.Fatalf("%v returned an empty body", header)
		}
		assertNonceMatchesBody(t, rr)
	}
}

// assertNonceMatchesBody requires the nonce the policy names to be the nonce the
// body carries on every script element.
func assertNonceMatchesBody(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	policy := rr.Header().Get("Content-Security-Policy")
	if !strings.HasPrefix(policy, "script-src 'nonce-") {
		t.Fatalf("policy = %q", policy)
	}
	rest := strings.TrimPrefix(policy, "script-src 'nonce-")
	nonce := rest[:strings.Index(rest, "'")]
	body := rr.Body.String()
	if !strings.Contains(body, `nonce="`+nonce+`"`) {
		t.Fatalf("body does not carry the policy's nonce %q: %s", nonce, body)
	}
	for _, other := range strings.Split(body, `nonce="`)[1:] {
		if !strings.HasPrefix(other, nonce) {
			t.Fatalf("body carries a second nonce: %s", other[:20])
		}
	}
}

// A range request would hand the client a slice of a body whose nonce may not be
// the one it has, and there is no validator to make a partial response safe, so
// the document is always served whole.
func TestEmbeddedPanelServesWholeDocument(t *testing.T) {
	s := newEmbeddedService(t, embeddedPanelBody)

	head := serve(t, s, http.MethodHead, "/management.html", nil)
	if head.Code != http.StatusOK {
		t.Fatalf("head status = %d, want %d", head.Code, http.StatusOK)
	}
	if got, want := head.Header().Get("Content-Length"), strconv.Itoa(len(embeddedPanelBody)); got != want {
		t.Fatalf("head content length = %q, want %q", got, want)
	}
	if head.Body.Len() != 0 {
		t.Fatalf("head body length = %d, want 0", head.Body.Len())
	}

	ranged := serve(t, s, http.MethodGet, "/management.html", http.Header{"Range": {"bytes=0-5"}})
	if ranged.Code != http.StatusOK {
		t.Fatalf("range status = %d, want 200 (always the whole document)", ranged.Code)
	}
	if ranged.Body.String() != embeddedPanelBody {
		t.Fatalf("range body = %q, want the whole document", ranged.Body.String())
	}
	if got := ranged.Header().Get("Accept-Ranges"); got != "" {
		t.Fatalf("accept ranges = %q, want none", got)
	}
}

func TestEmbeddedPanelReportsMissingContent(t *testing.T) {
	s := New("", fstest.MapFS{})
	rr := serve(t, s, http.MethodGet, "/management.html", nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

func TestPanelPathStillServesExternalFile(t *testing.T) {
	panelPath := filepath.Join(t.TempDir(), "management.html")
	external := "<html><body>external panel</body></html>"
	if err := os.WriteFile(panelPath, []byte(external), 0o600); err != nil {
		t.Fatalf("write panel: %v", err)
	}
	s := New(panelPath, fstest.MapFS{embeddedPanelFile: &fstest.MapFile{Data: []byte(embeddedPanelBody)}})

	rr := serve(t, s, http.MethodGet, "/management.html", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if rr.Body.String() != external {
		t.Fatalf("body = %q, want %q", rr.Body.String(), external)
	}
	if got, want := rr.Header().Get("Content-Length"), strconv.Itoa(len(external)); got != want {
		t.Fatalf("content length = %q, want %q", got, want)
	}
	// The document always carries the nonce its own response named, so it is
	// never revalidated: no validator, no reusable cache entry.
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("external panel cache control = %q, want no-store", got)
	}
	if got := rr.Header().Get("Last-Modified"); got != "" {
		t.Fatalf("external panel last modified = %q, want none", got)
	}
	if got := rr.Header().Get("ETag"); got != "" {
		t.Fatalf("external panel etag = %q, want none", got)
	}
}

func TestPanelPathFallsBackToEmbeddedWhenMissing(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "absent.html"), fstest.MapFS{embeddedPanelFile: &fstest.MapFile{Data: []byte(embeddedPanelBody)}})

	rr := serve(t, s, http.MethodGet, "/management.html", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if rr.Body.String() != embeddedPanelBody {
		t.Fatalf("body = %q, want %q", rr.Body.String(), embeddedPanelBody)
	}
	if got := rr.Header().Get("ETag"); got != "" {
		t.Fatalf("fallback response etag = %q, want none", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("fallback cache control = %q, want no-store", got)
	}
}
