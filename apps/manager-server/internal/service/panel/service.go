package panel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"
)

const embeddedPanelPath = "web/management.html"

type Service struct {
	PanelPath string
	Embedded  fs.FS

	embeddedData        []byte
	embeddedErr         error
	embeddedETag        string
	embeddedContentType string
	// embeddedStamped is the nonce-stamped copy of the embedded document and
	// embeddedPolicy the policy that matches it. Both are built once, because the
	// embedded artifact is fixed for the process lifetime. The nonce is
	// regenerated per response from embeddedPolicyTemplate.
	embeddedPolicyTemplate string
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
	if s.embeddedErr == nil {
		sum := sha256.Sum256(s.embeddedData)
		s.embeddedETag = `"` + hex.EncodeToString(sum[:16]) + `"`
	}
	return s
}

func (s *Service) ServeManagementHTML(w http.ResponseWriter, r *http.Request, writeError func(http.ResponseWriter, int, error)) {
	if s.PanelPath != "" {
		if document, info, err := s.readPanelFile(); err == nil {
			s.serveDocument(w, r, document, "management.html", info.ModTime(), "text/html; charset=utf-8")
			return
		}
	}
	if s.embeddedErr != nil {
		writeError(w, http.StatusInternalServerError, s.embeddedErr)
		return
	}
	// The embedded panel is fixed for the process lifetime and has no real file
	// modification time, so a content hash is the only stable validator here.
	w.Header().Set("ETag", s.embeddedETag)
	s.serveDocument(w, r, s.embeddedData, "management.html", time.Time{}, s.embeddedContentType)
}

// readPanelFile loads an operator-supplied panel document. It is read per
// request, because a file may be replaced on disk while the process runs, and
// because the nonce is per response regardless.
func (s *Service) readPanelFile() ([]byte, os.FileInfo, error) {
	file, err := os.Open(s.PanelPath)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	document, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, err
	}
	return document, info, nil
}

// serveDocument serves the legacy admin document under a per-response nonce CSP.
//
// The page holds admin authority, so it gets the same treatment as the Full Web
// Console document: every script element is stamped with a fresh nonce, and a
// document that cannot be stamped is served with `script-src 'none'` so it
// degrades to a blank page instead of running unconfined script.
func (s *Service) serveDocument(w http.ResponseWriter, r *http.Request, document []byte, name string, modified time.Time, contentType string) {
	if contentType == "" {
		contentType = "text/html; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	stamped, policy := applyLegacyPanelCSP(document)
	w.Header().Set("Content-Security-Policy", policy)
	http.ServeContent(w, r, name, modified, bytes.NewReader(stamped))
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
