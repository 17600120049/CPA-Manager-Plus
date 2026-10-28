package panel

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const embeddedPanelPath = "web/management.html"

type Service struct {
	PanelPath string
	Embedded  fs.FS

	embeddedData        []byte
	embeddedErr         error
	embeddedContentType string
}

func New(panelPath string, embedded fs.FS) *Service {
	s := &Service{
		PanelPath:           panelPath,
		Embedded:            embedded,
		embeddedContentType: htmlContentType(),
	}
	if embedded == nil {
		s.embeddedErr = fs.ErrNotExist
		return s
	}
	s.embeddedData, s.embeddedErr = fs.ReadFile(embedded, embeddedPanelPath)
	return s
}

func (s *Service) ServeManagementHTML(w http.ResponseWriter, r *http.Request, writeError func(http.ResponseWriter, int, error)) {
	if s.PanelPath != "" {
		if document, err := s.readPanelFile(); err == nil {
			s.serveDocument(w, r, document, "text/html; charset=utf-8")
			return
		}
	}
	if s.embeddedErr != nil {
		writeError(w, http.StatusInternalServerError, s.embeddedErr)
		return
	}
	s.serveDocument(w, r, s.embeddedData, s.embeddedContentType)
}

// readPanelFile loads an operator-supplied panel document. It is read per
// request, because a file may be replaced on disk while the process runs, and
// because the nonce is per response regardless.
func (s *Service) readPanelFile() ([]byte, error) {
	file, err := os.Open(s.PanelPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

// serveDocument serves the legacy admin document under a per-response nonce CSP.
//
// The page holds admin authority, so it gets the same treatment as the Full Web
// Console document: every script element is stamped with a fresh nonce, and a
// document that cannot be stamped is served with `script-src 'none'` so it
// degrades into a page that runs nothing rather than one that runs unconfined
// script.
//
// It is served `no-store` with no validator, and that is deliberate. The body
// carries the nonce its policy names, so a body is only ever valid together with
// the exact response that delivered it. A 304 revalidation would send a fresh
// nonce header against the browser's stored body, which still holds the previous
// nonce, and every script element would be blocked - a blank admin page. There
// is no validator to revalidate against at all; a cache may keep the document
// only for the request that fetched it.
func (s *Service) serveDocument(w http.ResponseWriter, r *http.Request, document []byte, contentType string) {
	if contentType == "" {
		contentType = "text/html; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	stamped, policy := applyLegacyPanelCSP(document)
	w.Header().Set("Content-Security-Policy", policy)
	// Serve the stamped body directly. http.ServeContent would add a
	// Last-Modified of its own and answer conditional requests with 304.
	w.Header().Set("Content-Length", strconv.Itoa(len(stamped)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(stamped)
}

func htmlContentType() string {
	contentType := mime.TypeByExtension(".html")
	if contentType == "" {
		contentType = "text/html"
	}
	if !strings.Contains(contentType, "charset=") {
		contentType += "; charset=utf-8"
	}
	return contentType
}
