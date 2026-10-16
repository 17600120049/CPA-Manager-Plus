package httpapi

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/routing"
)

func TestBrandResourcesUsePinnedBlobsWithoutAuthorityOrAuthentication(t *testing.T) {
	ledger, err := os.ReadFile("../../../../tests/fixtures/phase4-routing/route-owners.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Resources []struct{ Path, Blob, MIME, CacheControl string } `json:"fixedBrandResources"`
	}
	if err := json.Unmarshal(ledger, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Resources) != 2 {
		t.Fatal("exactly two resources required")
	}
	for _, asset := range fixture.Resources {
		data, err := fs.ReadFile(embeddedPanel, "web"+asset.Path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(data))), data...))
		if hex.EncodeToString(sum[:]) != asset.Blob {
			t.Fatalf("blob mismatch %s", asset.Path)
		}
		for _, method := range []string{"GET", "HEAD"} {
			r := httptest.NewRequest(method, asset.Path, nil)
			w := httptest.NewRecorder()
			ProtectBrandResources(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("fell through") })).ServeHTTP(w, r)
			if w.Code != 200 || w.Header().Get("Content-Type") != asset.MIME || w.Header().Get("Cache-Control") != asset.CacheControl || w.Header().Get("Location") != "" {
				t.Fatal(w.Code, w.Header())
			}
			if method == "HEAD" && w.Body.Len() != 0 {
				t.Fatal("HEAD has body")
			}
			if method == "GET" && !strings.EqualFold(hex.EncodeToString(w.Body.Bytes()), hex.EncodeToString(data)) {
				t.Fatal("asset body changed")
			}
		}
	}
}

func TestBrandMissingDisabledAndNegativeRequests(t *testing.T) {
	for _, assets := range []fs.FS{nil, fstest.MapFS{}} {
		for _, p := range []string{"/favicon.ico", "/apple-touch-icon.png"} {
			r := httptest.NewRequest("GET", p, nil)
			w := httptest.NewRecorder()
			brandHandler(assets).ServeHTTP(w, r)
			if w.Code != 404 || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), "<html") {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
	for _, method := range []string{"GET", "HEAD", "POST", "PUT"} {
		for _, p := range []string{"/favicon.ico", "/apple-touch-icon.png", "/favicon.ico/x", "/favicon.ico-lookalike", "/%66avicon.ico", "/favicon%2eico", "/favicon.ico/../favicon.ico", "/apple-touch-icon.png/", "/apple-touch-icon.png%2fchild"} {
			r := httptest.NewRequest(method, p, nil)
			if routing.ExactBrandResource(r) {
				continue
			}
			called := false
			h := ProtectBrandResources(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(418) }))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if !called || w.Code != 418 {
				t.Fatalf("exception leaked to %s %s", method, p)
			}
		}
	}
	for _, p := range []string{"/favicon.ico", "/favicon.ico/control", "/apple-touch-icon.png", "/apple-touch-icon.png/control"} {
		s := routing.RoutingSnapshot{State: routing.SnapshotVerifiedEffective, Revision: 1, VerifiedEffectiveControlBasePath: p}
		if s.Validate() == nil {
			t.Fatalf("brand collision accepted: %s", p)
		}
	}
}

// Required after the real build/staging path; ordinary source-only Go runs do
// not pretend an absent generated dist is a packaged artifact.
func TestPackagedFullWebDist(t *testing.T) {
	if os.Getenv("CPAMP_FULLWEB_PACKAGED_TEST") != "1" {
		t.Skip("run after npm run build with CPAMP_FULLWEB_PACKAGED_TEST=1")
	}
	h := FullWebHandler(routing.RoutingSnapshot{State: routing.SnapshotVerifiedEffective, VerifiedEffectiveControlBasePath: "/phase4-test", Revision: 1}, nil)
	r := httptest.NewRequest("GET", "/phase4-test/technical-route", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("packaged HTML %d", w.Code)
	}
	refs := regexp.MustCompile(`(?:src|href)="(/phase4-test/assets/[^"]+)"`).FindAllStringSubmatch(w.Body.String(), -1)
	if len(refs) == 0 {
		t.Fatal("packaged HTML has no hashed assets")
	}
	for _, ref := range refs {
		asset := httptest.NewRecorder()
		h.ServeHTTP(asset, httptest.NewRequest("GET", ref[1], nil))
		if asset.Code != 200 || asset.Body.Len() == 0 || strings.Contains(asset.Header().Get("Content-Type"), "text/html") || !strings.Contains(asset.Header().Get("Cache-Control"), "immutable") {
			t.Fatal(ref[1], asset.Code, asset.Header())
		}
	}
}

// Optional local-only browser carrier using the same compiled embed. No
// production flag, configuration source, database or Bootstrap flow exists.
func TestFullWebBrowserCarrier(t *testing.T) {
	addr := os.Getenv("CPAMP_BROWSER_CARRIER_ADDR")
	if addr == "" {
		t.Skip("explicit non-production browser carrier only")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	full := FullWebHandler(routing.RoutingSnapshot{State: routing.SnapshotVerifiedEffective, VerifiedEffectiveControlBasePath: "/phase4-test", Revision: 1}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/phase4-test/api/v2/technical-probe" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(`{"technical":true}`))
	}))
	h := ProtectBrandResources(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panel/management.html" {
			data, err := os.ReadFile("../../../web/dist/panel/management.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			w.Write(data)
			return
		}
		full.ServeHTTP(w, r)
	}))
	server := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go server.Serve(listener)
	t.Logf("NON-PRODUCTION carrier http://%s/phase4-test/technical-route", listener.Addr())
	<-time.After(3 * time.Minute)
}
