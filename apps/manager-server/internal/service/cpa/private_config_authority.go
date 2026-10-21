package cpa

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

const privateConfigurationPath = "/v1/runtime/private-configuration"

func hashBytes(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, ErrProofUnavailable
	}
	return b, nil
}
func strictJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return ErrProofUnavailable
	}
	return nil
}

type configOperation struct {
	KeeperSHA256 string `json:"keeperSHA256,omitempty"`
	ID           string `json:"id"`
	Scope        string `json:"scope"`
	KeySHA256    string `json:"keySHA256,omitempty"`
	Retry        int    `json:"retry"`
	Generation   string `json:"generation"`
	State        string `json:"state"`
}
type configRequest struct {
	H1               *H1Request `json:"h1,omitempty"`
	Action           string     `json:"action"`
	SourceID         string     `json:"sourceId"`
	Generation       string     `json:"generation"`
	ExpectedRevision uint64     `json:"expectedRevision"`
	ExpectedSHA256   string     `json:"expectedSHA256"`
	OperationID      string     `json:"operationId"`
	Scope            string     `json:"scope"`
	KeySHA256        string     `json:"keySHA256,omitempty"`
	Retry            int        `json:"retry"`
}
type configObservation struct {
	H1             *H1Record       `json:"h1,omitempty"`
	SourceID       string          `json:"sourceId"`
	Generation     string          `json:"generation"`
	Revision       uint64          `json:"revision"`
	SHA256         string          `json:"sha256"`
	State          string          `json:"state"`
	Installed      bool            `json:"installed"`
	CommittedRetry *int            `json:"committedRetry"`
	Operation      configOperation `json:"operation"`
	Created        bool            `json:"created"`
}
type configGrant struct {
	BootstrapRun      string   `json:"bootstrapRun,omitempty"`
	H1                *H1Grant `json:"h1,omitempty"`
	SourceID          string   `json:"sourceId"`
	Generation        string   `json:"generation"`
	Namespace         string   `json:"namespace"`
	Device            uint64   `json:"device"`
	Inode             uint64   `json:"inode"`
	SeedSHA256        string   `json:"seedSHA256"`
	IssuedAtMS        int64    `json:"issuedAtMs"`
	ExpiresAtMS       int64    `json:"expiresAtMs"`
	RecoveryOperation string   `json:"recoveryOperation,omitempty"`
	RecoverySHA256    string   `json:"recoverySHA256,omitempty"`
}
type privateConfigClient struct {
	call  func(context.Context, configRequest) (configObservation, error)
	grant func() (configGrant, error)
}

func newPrivateConfigClient() *privateConfigClient {
	if os.Getenv("CPAMP_PRIVATE_CONFIG_AUTHORITY") != "1" {
		// A mounted Host source must never silently use the uncoordinated S1
		// compatibility transport when one half of the profile is misconfigured.
		_, err := os.Lstat("/run/cpamp-config/source.json")
		if os.Getenv("CPAMP_PRIVATE_CONFIG_AUTHORITY") != "" || !os.IsNotExist(err) {
			return &privateConfigClient{grant: func() (configGrant, error) { return configGrant{}, ErrProofUnavailable }}
		}
		return nil
	}
	direct := http.DefaultTransport.(*http.Transport).Clone()
	direct.Proxy = nil
	direct.DisableKeepAlives = true
	direct.MaxResponseHeaderBytes = 16 << 10
	client := &http.Client{Transport: direct, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &privateConfigClient{grant: loadConfigGrant, call: func(ctx context.Context, q configRequest) (configObservation, error) {
		var v configObservation
		token, e := readConfigHostFile("/run/cpamp/runtime-secret/token")
		if e != nil {
			return v, ErrProofUnavailable
		}
		b, e := json.Marshal(q)
		if e != nil {
			return v, ErrProofUnavailable
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:9081"+privateConfigurationPath, bytes.NewReader(b))
		if e != nil {
			return v, ErrProofUnavailable
		}
		req.Header.Set("Authorization", "Bearer "+string(token))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cache-Control", "no-store")
		res, e := client.Do(req)
		if e != nil {
			return v, ErrApplyUnknown
		}
		defer res.Body.Close()
		if res.StatusCode != 200 || res.Header.Get("Content-Encoding") != "" {
			return v, ErrSourceConflict
		}
		data, e := readBounded(res.Body, 16384)
		if e != nil || strictJSON(data, &v) != nil || v.SourceID != q.SourceID || v.Generation != q.Generation ||
			!validHex(v.SHA256) || v.Revision == 0 || (v.State != "persisted" && v.State != "unknown") {
			return configObservation{}, ErrProofUnavailable
		}
		return v, nil
	}}
}
func loadConfigGrant() (configGrant, error) {
	var g configGrant
	b, e := readConfigHostFile("/run/cpamp-config/source.json")
	if e != nil || strictJSON(b, &g) != nil {
		return g, ErrProofUnavailable
	}
	now := time.Now().UnixMilli()
	if len(g.SourceID) < 16 || g.Generation == "" || g.IssuedAtMS <= 0 || g.IssuedAtMS > now ||
		g.ExpiresAtMS <= now || g.ExpiresAtMS-g.IssuedAtMS > 600000 {
		return g, ErrProofUnavailable
	}
	return g, nil
}
func (t *Transport) configuration(ctx context.Context, action string) (configRequest, configObservation, error) {
	var q configRequest
	var v configObservation
	if t.configurationAuthority == nil {
		return q, v, ErrProofUnavailable
	}
	g, e := t.configurationAuthority.grant()
	if e != nil {
		return q, v, e
	}
	q = configRequest{Action: action, SourceID: g.SourceID, Generation: g.Generation}
	v, e = t.configurationAuthority.call(ctx, q)
	if e == nil && v.H1 != nil && t.h1 == nil {
		return q, configObservation{}, ErrProofUnavailable
	}
	return q, v, e
}
func (t *Transport) beginInitialConfiguration(ctx context.Context, proof TargetProof, secret string) (*configRequest, error) {
	if t.configurationAuthority == nil {
		return nil, nil
	}
	q, v, e := t.configuration(ctx, "observe")
	if e != nil || q.Generation != strconv.FormatUint(proof.Generation, 10) || v.State != "persisted" || v.Installed {
		return nil, ErrSourceConflict
	}
	q.Action = "begin"
	q.ExpectedRevision = v.Revision
	q.ExpectedSHA256 = v.SHA256
	q.Scope = "initial-key"
	q.OperationID = "s1-" + hashBytes([]byte(secret))
	q.KeySHA256 = hashBytes([]byte(secret))
	begun, e := t.configurationAuthority.call(ctx, q)
	if e != nil || !begun.Created || begun.State != "unknown" || begun.Operation.ID != q.OperationID {
		return nil, ErrApplyUnknown
	}
	// The Host source lease can also expire while durable reservation waits.
	// Never turn that unknown intention into a PUT under a stale grant.
	g, e := t.configurationAuthority.grant()
	if e != nil || g.SourceID != q.SourceID || g.Generation != q.Generation {
		return nil, ErrApplyUnknown
	}
	q.ExpectedRevision = begun.Revision
	return &q, nil
}
func (t *Transport) commitConfiguration(ctx context.Context, q *configRequest) error {
	if q == nil {
		return nil
	}
	_, observed, e := t.configuration(ctx, "observe")
	if e != nil || observed.Operation.ID != q.OperationID || observed.Revision != q.ExpectedRevision {
		return ErrApplyUnknown
	}
	commit := *q
	commit.Action = "commit"
	commit.ExpectedSHA256 = observed.SHA256
	v, e := t.configurationAuthority.call(ctx, commit)
	if e != nil || v.State != "persisted" || v.Operation.ID != q.OperationID || v.Operation.State != "persisted" {
		return ErrApplyUnknown
	}
	return nil
}

// reconcilePrivateConfiguration performs reads plus terminal evidence recording,
// never a second CPA mutation. A pre-write/incomplete result stays unknown.
func (t *Transport) reconcilePrivateConfiguration(ctx context.Context, operationID string) error {
	q, v, e := t.configuration(ctx, "observe")
	if e != nil || v.Operation.ID != operationID {
		return ErrSourceConflict
	}
	if v.State == "persisted" && v.Operation.State == "persisted" {
		return nil
	}
	q.OperationID = operationID
	q.ExpectedRevision = v.Revision
	return t.commitConfiguration(ctx, &q)
}

// committedConfiguration asks Runtime to check the complete admitted file.
// Official ConfigV8 YAML GET normalizes layout and is not a raw-byte receipt.
// Existing S1/Provider validators stay unchanged; only a durably committed
// post-install retry leaf may be removed from their projection.
func (t *Transport) committedConfiguration(ctx context.Context) (configObservation, error) {
	_, v, e := t.configuration(ctx, "observe")
	if e != nil || !v.Installed || v.State != "persisted" {
		return v, ErrSourceConflict
	}
	return v, nil
}
func (t *Transport) postInstallProjection(ctx context.Context, body []byte) ([]byte, error) {
	if t.configurationAuthority == nil {
		return body, nil
	}
	v, e := t.committedConfiguration(ctx)
	if e != nil {
		return nil, e
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return nil, ErrSourceConflict
	}
	routing, exists := root["routing"]
	if !exists {
		if v.CommittedRetry != nil {
			return nil, ErrSourceConflict
		}
		return body, nil
	}
	if v.CommittedRetry == nil {
		return nil, ErrSourceConflict
	}
	var r, retry map[string]json.RawMessage
	var value *int
	if json.Unmarshal(routing, &r) != nil || len(r) != 1 || json.Unmarshal(r["retry"], &retry) != nil || len(retry) != 1 ||
		json.Unmarshal(retry["request-retry"], &value) != nil || value == nil || *value != *v.CommittedRetry {
		return nil, ErrSourceConflict
	}
	delete(root, "routing")
	return json.Marshal(root)
}
