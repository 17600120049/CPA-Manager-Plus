package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeyapply"
)

const privateChatAdmissionPath = "/internal/fresh-chat-admission"

// No public router/middleware wraps this loopback-only proof consumer. Public
// Fresh AND Legacy Ingress classify this namespace as deny before proxying.
func (a *freshApplication) privateChatAdmission(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s2a, s2b := os.Getenv("CPAMP_PRIVATE_S2A_CHAT") == "1", os.Getenv("CPAMP_PRIVATE_S2B_CHAT") == "1"
	if s2a == s2b || a.keyApply == nil || !privateProofLoopback(r.RemoteAddr) ||
		r.Method != http.MethodPost || r.URL.EscapedPath() != privateChatAdmissionPath || r.URL.RawQuery != "" || r.URL.ForceQuery ||
		r.Header.Get("Content-Type") != "application/json" || len(r.Header.Values("Authorization")) != 1 {
		w.WriteHeader(404)
		return
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || len(auth) != 80 {
		w.WriteHeader(503)
		return
	}
	var body struct {
		RequestBinding string `json:"requestBinding"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256))
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || d.Decode(&struct{}{}) != io.EOF {
		w.WriteHeader(503)
		return
	}
	op, err := a.current(r.Context())
	if err != nil || op.Strategy.Stage != "committed" || op.Strategy.Strategy != bootstrap.NewManaged ||
		op.Strategy.TargetIdentity != cpa.PrivateKeyRuntime || op.Strategy.TargetArtifactID != cpa.PrivateKeyArtifact {
		w.WriteHeader(503)
		return
	}
	verdict, err := a.keyApply.Admit(r.Context(), auth[7:], op.Strategy.TargetGeneration, body.RequestBinding)
	if err != nil {
		if errors.Is(err, freshkeyapply.ErrGeneration) {
			w.WriteHeader(409)
		} else {
			w.WriteHeader(503)
		}
		return
	}
	// The committed Fresh target must still be the one just observed.
	current, err := a.current(r.Context())
	if err != nil || current.Identity != op.Identity || current.Revision != op.Revision {
		w.WriteHeader(503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeFreshJSON(w, verdict)
}
