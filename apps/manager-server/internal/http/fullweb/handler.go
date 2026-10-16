// Package fullweb serves the complete Full Web artifact using the Phase4-01
// snapshot authority. It does not load configuration or activate production.
package fullweb

import (
	"bytes"
	"encoding/json"
	"html"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/routing"
)

type Handler struct {
	Snapshot routing.RoutingSnapshot
	Dist     fs.FS
	API      http.Handler
}

var hashedAsset = regexp.MustCompile(`^assets/.+-[A-Za-z0-9_-]{8,}\.[a-z0-9]+$`)

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	prefix, ok := h.Snapshot.EffectiveControlBasePath()
	if !ok || r == nil || r.URL == nil {
		http.NotFound(w, r)
		return
	}
	requestPath := r.URL.Path
	if !routing.CanonicalRequestPath(requestPath) || r.URL.EscapedPath() != requestPath ||
		(r.RequestURI != "" && strings.SplitN(r.RequestURI, "?", 2)[0] != requestPath) ||
		(requestPath != prefix && !strings.HasPrefix(requestPath, prefix+"/")) {
		http.NotFound(w, r)
		return
	}
	apiBase, _ := h.Snapshot.CanonicalPublicAPIBase()
	if requestPath == apiBase || strings.HasPrefix(requestPath, apiBase+"/") {
		routing.PublicAPIHandler{Snapshot: h.Snapshot, Active: h.API}.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	suffix := strings.TrimPrefix(requestPath, prefix+"/")
	if suffix == requestPath {
		suffix = ""
	}
	// All API-looking paths remain failures, including malformed API versions.
	if suffix == "api" || strings.HasPrefix(suffix, "api/") {
		http.NotFound(w, r)
		return
	}
	if suffix == "assets" || strings.HasPrefix(suffix, "assets/") {
		h.serveAsset(w, r, suffix)
		return
	}
	// Only HTML navigation can receive SPA fallback. File-like and missing
	// asset requests never become HTML success, regardless of Accept.
	if path.Ext(suffix) != "" || !strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.NotFound(w, r)
		return
	}
	if h.Dist == nil {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(h.Dist, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	context, _ := json.Marshal(struct {
		State    routing.SnapshotState `json:"state"`
		Base     string                `json:"verifiedEffectiveControlBasePath"`
		Revision uint64                `json:"revision"`
	}{h.Snapshot.State, prefix, h.Snapshot.Revision})
	data = bytes.ReplaceAll(data, []byte("__CPAMP_ROUTING_CONTEXT__"), context)
	// Vite base './' keeps lazy imports relative to their module, while entry
	// references are rebound once here for pathname deep-link refresh.
	data = bytes.ReplaceAll(data, []byte(`"./assets/`), []byte(`"`+html.EscapeString(prefix)+`/assets/`))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(data))
}

func (h Handler) serveAsset(w http.ResponseWriter, r *http.Request, name string) {
	if h.Dist == nil || !hashedAsset.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(h.Dist, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
