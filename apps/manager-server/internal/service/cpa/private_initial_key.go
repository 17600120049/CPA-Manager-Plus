package cpa

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	privateCPAArtifact     = "sha256:b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41"
	privateRuntimeIdentity = "embedded-auth-proof"
	privateCPAAddress      = "127.0.0.1:8317"
	maxResponseBytes       = 1 << 20
)

var (
	ErrProofUnavailable = errors.New("private key application proof unavailable")
	ErrSourceConflict   = errors.New("CPA key source is not the isolated empty proof seed")
	ErrApplyUnknown     = errors.New("CPA key write result unknown; do not retry")
)

// TargetProof is a caller-observed runtime identity. The caller must separately
// prove disposable empty volumes, the exclusive writer and live process.
// These flags are NOT host or network attestation by themselves.
type TargetProof struct {
	Identity        string
	Generation      uint64
	ArtifactID      string
	Ready           bool
	SourceExclusive bool
	Disposable      bool
	ExpiresAtMS     int64
	ProviderSeed    PrivateProviderSeed
}

// InstallResult never exposes secrets or asserts Runtime effectiveness.
type InstallResult struct {
	CPAConfigured     bool   `json:"cpaConfigured"`
	RuntimeState      string `json:"runtimeState"`
	GatewayAuthorized bool   `json:"gatewayAuthorized"`
}

// Transport pins production requests to the private loopback CPA Management
// endpoint. Tests can inject a private synthetic server inside this package.
type Transport struct {
	mu           sync.Mutex
	client       *http.Client
	endpoint     string
	attempted    bool
	providerSeed func() (PrivateProviderSeed, error)
}

func NewTransport() *Transport {
	direct := http.DefaultTransport.(*http.Transport).Clone()
	direct.Proxy = nil
	direct.DisableKeepAlives = true
	direct.MaxResponseHeaderBytes = 32 << 10
	direct.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", privateCPAAddress)
	}
	return &Transport{client: &http.Client{
		Transport:     direct,
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, endpoint: "http://" + privateCPAAddress}
}

func (t *Transport) request(ctx context.Context, method, path, managementKey string, payload []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, t.endpoint+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, ErrProofUnavailable
	}
	if managementKey != "" {
		req.Header.Set("Authorization", "Bearer "+managementKey)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-store")
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return 0, nil, ErrProofUnavailable
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
		return 0, nil, ErrProofUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return 0, nil, ErrProofUnavailable
	}
	return resp.StatusCode, body, nil
}

// InstallOne makes at most one mutation and never retries. Its caller must
// separately verify process/generation, empty volumes and sole source/writer.
func (t *Transport) InstallOne(ctx context.Context, proof TargetProof, managementKey, stagedSecret string) (InstallResult, error) {
	unknown := InstallResult{RuntimeState: "effect_unknown", GatewayAuthorized: false}
	if t == nil {
		return unknown, ErrProofUnavailable
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.attempted {
		return unknown, ErrApplyUnknown
	}
	if t.client == nil || !proof.Disposable || !proof.SourceExclusive || !proof.Ready ||
		proof.ExpiresAtMS <= time.Now().UnixMilli() ||
		proof.Generation == 0 || proof.Identity != privateRuntimeIdentity || proof.ArtifactID != privateCPAArtifact ||
		len(managementKey) < 32 || len(managementKey) > 72 || len(stagedSecret) != 73 ||
		stagedSecret[:9] != "cpamp_sk_" || !validHex(stagedSecret[9:]) || !printable(managementKey) {
		return unknown, ErrProofUnavailable
	}
	status, full, err := t.request(ctx, http.MethodGet, "/v8/management/config", managementKey, nil)
	validSeed := emptyProofConfig(full) && proof.ProviderSeed == (PrivateProviderSeed{})
	if t.providerSeed != nil {
		seed, sourceErr := t.providerSeed()
		validSeed = sourceErr == nil && seed == proof.ProviderSeed && privateProviderConfig(full, seed, "")
	}
	if err != nil || status != http.StatusOK || !validSeed {
		return unknown, ErrSourceConflict
	}
	status, current, err := t.request(ctx, http.MethodGet, "/v8/management/config/access/api-keys", managementKey, nil)
	if err != nil || status != http.StatusOK || !emptyStringList(current) {
		return unknown, ErrSourceConflict
	}
	payload, err := json.Marshal([]string{stagedSecret})
	if err != nil {
		return unknown, ErrProofUnavailable
	}
	// After the PUT attempt, failed/unknown outcomes remain unknown: no retry.
	if proof.ExpiresAtMS <= time.Now().UnixMilli() {
		return unknown, ErrProofUnavailable
	}
	t.attempted = true
	status, _, err = t.request(ctx, http.MethodPut, "/v8/management/config/access/api-keys", managementKey, payload)
	if err != nil || status != http.StatusOK {
		return unknown, ErrApplyUnknown
	}
	status, written, err := t.request(ctx, http.MethodGet, "/v8/management/config/access/api-keys", managementKey, nil)
	if err != nil || status != http.StatusOK {
		return unknown, ErrApplyUnknown
	}
	var list []string
	if json.Unmarshal(written, &list) != nil || len(list) != 1 || list[0] != stagedSecret {
		return unknown, ErrApplyUnknown
	}
	// Config readback is not a live CPA authentication acknowledgment.
	return InstallResult{CPAConfigured: true, RuntimeState: "effect_unknown", GatewayAuthorized: false}, nil
}

func emptyProofConfig(data []byte) bool {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil || (len(root) != 7 && len(root) != 8) {
		return false
	}
	if len(root) == 8 {
		// Only this fixed loopback synthetic provider may accompany the seed.
		var actual, expected any
		if json.Unmarshal(root["api-keys"], &actual) != nil || json.Unmarshal([]byte(privateSyntheticProvider), &expected) != nil {
			return false
		}
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(expected)
		if !bytes.Equal(a, b) {
			return false
		}
	}
	for _, name := range []string{"config-version", "server", "management", "access", "oauth", "plugins", "observability"} {
		if _, ok := root[name]; !ok {
			return false
		}
	}
	var version int
	if json.Unmarshal(root["config-version"], &version) != nil || version != 8 {
		return false
	}
	var server map[string]json.RawMessage
	if json.Unmarshal(root["server"], &server) != nil || len(server) != 2 {
		return false
	}
	var host string
	var port int
	if json.Unmarshal(server["host"], &host) != nil || host != "127.0.0.1" ||
		json.Unmarshal(server["port"], &port) != nil || port != 8317 {
		return false
	}
	var access map[string]json.RawMessage
	if json.Unmarshal(root["access"], &access) != nil || len(access) != 1 || !emptyStringList(access["api-keys"]) {
		return false
	}
	var oauth map[string]json.RawMessage
	if json.Unmarshal(root["oauth"], &oauth) != nil || len(oauth) != 1 {
		return false
	}
	var authDir string
	if json.Unmarshal(oauth["auth-dir"], &authDir) != nil || authDir != "/runtime/gateway/auth" {
		return false
	}
	var management map[string]json.RawMessage
	if json.Unmarshal(root["management"], &management) != nil || len(management) != 4 {
		return false
	}
	for name, expected := range map[string]bool{"allow-remote": false, "disable-control-panel": true, "disable-auto-update-panel": true} {
		var value bool
		if (!bytes.Equal(management[name], []byte("true")) && !bytes.Equal(management[name], []byte("false"))) || json.Unmarshal(management[name], &value) != nil || value != expected {
			return false
		}
	}
	var enabled bool
	var plugins map[string]json.RawMessage
	var pluginDir string
	if json.Unmarshal(root["plugins"], &plugins) != nil || len(plugins) != 2 ||
		!bytes.Equal(plugins["enabled"], []byte("false")) || json.Unmarshal(plugins["enabled"], &enabled) != nil || enabled ||
		json.Unmarshal(plugins["dir"], &pluginDir) != nil || pluginDir != "/runtime/gateway/plugins" {
		return false
	}
	var observability struct {
		Logs struct {
			LoggingToFile bool `json:"logging-to-file"`
		} `json:"logs"`
		Usage struct {
			UsageStatisticsEnabled bool `json:"usage-statistics-enabled"`
		} `json:"usage"`
	}
	if json.Unmarshal(root["observability"], &observability) != nil ||
		observability.Logs.LoggingToFile || observability.Usage.UsageStatisticsEnabled {
		return false
	}
	var obs map[string]json.RawMessage
	if json.Unmarshal(root["observability"], &obs) != nil || len(obs) != 2 {
		return false
	}
	for _, sub := range []string{"logs", "usage"} {
		var nested map[string]json.RawMessage
		if json.Unmarshal(obs[sub], &nested) != nil || len(nested) != 1 {
			return false
		}
		name := map[string]string{"logs": "logging-to-file", "usage": "usage-statistics-enabled"}[sub]
		if !bytes.Equal(nested[name], []byte("false")) {
			return false
		}
	}
	var secret string
	return json.Unmarshal(management["secret-key"], &secret) == nil && len(secret) > 0
}
func emptyStringList(data []byte) bool {
	var v []string
	return json.Unmarshal(data, &v) == nil && v != nil && len(v) == 0
}
func validHex(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}
func printable(s string) bool {
	for _, c := range s {
		if c < '!' || c > '~' {
			return false
		}
	}
	return true
}
