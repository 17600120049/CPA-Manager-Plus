package ingress

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"
)

type chatVerdict struct {
	RequestBinding string `json:"requestBinding"`
	ExpiresAtMS    int64  `json:"expiresAtMs"`
	Model          string `json:"model,omitempty"`
}
type privateChat struct {
	manager, gateway string
	model            string
	client           *http.Client
	transport        *http.Transport
}

// EnablePrivateChat has no default/general-deployment mode. The host permit and
// committed Fresh strategy remain Manager's authority on every actual request.
func (h *Handler) EnablePrivateChat(manager, gateway *url.URL) {
	if manager.String() == "http://127.0.0.1:18319" && gateway.String() == "http://127.0.0.1:8317" {
		h.chat = newPrivateChat(manager.String(), gateway.String())
	}
}

// EnablePrivateS2BChat fixes one model at trusted process startup. There is no
// request-driven target/model discovery and no change to S2-A's synthetic model.
func (h *Handler) EnablePrivateS2BChat(manager, gateway *url.URL, model string) bool {
	if model == "" || model == "s1-proof-model" || len(model) > 128 {
		return false
	}
	for _, c := range model {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '/') {
			return false
		}
	}
	h.EnablePrivateChat(manager, gateway)
	if h.chat == nil {
		return false
	}
	h.chat.model = model
	return true
}

func newPrivateChat(manager, gateway string) *privateChat {
	direct := http.DefaultTransport.(*http.Transport).Clone()
	direct.Proxy = nil
	direct.DisableKeepAlives = true
	direct.ForceAttemptHTTP2 = false
	direct.MaxResponseHeaderBytes = 32 << 10
	upstream := direct.Clone()
	upstream.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		attempt, ok := ctx.Value(chatDeadlineKey{}).(*chatAttempt)
		if !ok || time.Now().UnixMilli() >= attempt.until {
			return nil, errChat
		}
		dialCtx, cancel := context.WithDeadline(ctx, time.UnixMilli(attempt.until))
		defer cancel()
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(dialCtx, network, address)
		if err != nil {
			return nil, errChat
		}
		return &admittedConn{Conn: conn, until: attempt.until, wrote: &attempt.wrote}, nil
	}
	return &privateChat{manager: manager + PrivateChatAdmissionPath, gateway: gateway + chatPath, model: "s1-proof-model",
		client: &http.Client{Transport: direct, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport: upstream}
}

type chatDeadlineKey struct{}
type chatAttempt struct {
	until int64
	wrote atomic.Bool
}
type admittedConn struct {
	net.Conn
	until   int64
	started bool
	wrote   *atomic.Bool
}

func (c *admittedConn) Write(b []byte) (int, error) {
	if !c.started {
		// Last gate, after dialing and immediately before any CPA request byte.
		if time.Now().UnixMilli() >= c.until {
			return 0, errChat
		}
		c.started = true
	}
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.wrote.Store(true)
	}
	return n, err
}

func (h *privateChat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	deny := func() { http.Error(w, "gateway pending configuration", 503) }
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(2 * time.Second))
	body, err := readChatModel(r, h.model)
	_ = controller.SetReadDeadline(time.Time{})
	if err != nil {
		deny()
		return
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		deny()
		return
	}
	hash := sha256.New()
	hash.Write(nonce[:])
	hash.Write([]byte(r.Header.Get("Authorization")))
	hash.Write(body)
	binding := hex.EncodeToString(hash.Sum(nil))
	payload, _ := json.Marshal(struct {
		RequestBinding string `json:"requestBinding"`
	}{binding})
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.manager, bytes.NewReader(payload))
	if err != nil {
		deny()
		return
	}
	request.Header.Set("Authorization", r.Header.Get("Authorization"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cache-Control", "no-store")
	response, err := h.client.Do(request)
	if err != nil {
		deny()
		return
	}
	var verdict chatVerdict
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 1025))
	response.Body.Close()
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	valid := readErr == nil && len(data) <= 1024 && response.StatusCode == 200 && response.Header.Get("Content-Type") == "application/json" &&
		d.Decode(&verdict) == nil && d.Decode(new(any)) == io.EOF
	expectedModel := h.model
	if expectedModel == "s1-proof-model" {
		expectedModel = ""
	}
	if !valid || verdict.Model != expectedModel || verdict.RequestBinding != binding || verdict.ExpiresAtMS <= time.Now().UnixMilli() || verdict.ExpiresAtMS > time.Now().Add(time.Second).UnixMilli() {
		deny()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	attempt := &chatAttempt{until: verdict.ExpiresAtMS}
	ctx = context.WithValue(ctx, chatDeadlineKey{}, attempt)
	forward, err := http.NewRequestWithContext(ctx, http.MethodPost, h.gateway, bytes.NewReader(body))
	if err != nil {
		deny()
		return
	}
	// Only the original inspected Bearer and bytes reach CPA. No client-supplied
	// management, routing, alternate-auth or forwarding headers cross this edge.
	forward.Header.Set("Authorization", r.Header.Get("Authorization"))
	forward.Header.Set("Content-Type", "application/json")
	forward.Header.Set("Accept-Encoding", "identity")
	forward.GetBody = nil // no transparent replay of this admission
	result, err := h.transport.RoundTrip(forward)
	if err != nil {
		if attempt.wrote.Load() {
			http.Error(w, "upstream request failed", 502)
			return
		}
		deny()
		return
	}
	defer result.Body.Close()
	// A request already written to CPA remains a real arrival, even if cancelled
	// or its response fails. Never retry or relabel it as pre-admission rejection.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(result.StatusCode)
	_, _ = io.Copy(w, result.Body)
}
