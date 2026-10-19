package main

import (
	"net"
	"net/http"
	"os"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
)

const privateStagedKeyProofPath = "/internal/fresh-client-key-stage/probe"

// This opt-in loopback-only diagnostic checks one Manager-staged credential.
// It is NOT a Runtime-effective authorization response and MUST NOT be used
// to enable or forward model requests. It is absent by default.
func (a *freshApplication) privateStagedKeyProof(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.EscapedPath() != privateStagedKeyProofPath || r.URL.RawQuery != "" ||
		os.Getenv("CPAMP_PRIVATE_CLIENT_AUTH_PROOF") != "1" || !privateProofLoopback(r.RemoteAddr) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	matched, err := a.keys.MatchStagedSecret(r.Context(), security.ExtractBearerToken(r.Header.Get("Authorization")))
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !matched {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeFreshJSON(w, struct {
		ManagerStagedKeyMatched bool `json:"managerStagedKeyMatched"`
		RuntimeEffective        bool `json:"runtimeEffective"`
		GatewayAuthorized       bool `json:"gatewayAuthorized"`
	}{ManagerStagedKeyMatched: true})
}

func privateProofLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
