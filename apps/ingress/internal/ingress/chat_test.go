package ingress

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const chatBody = `{"model":"s1-proof-model","stream":false,"messages":[{"role":"user","content":"proof"}]}`

func TestPrivateChatBodyAndURLDenyBeforeAuthority(t *testing.T) {
	for _, body := range []string{
		`{}`, `null`, chatBody + `{}`, chatBody + `x`, strings.Replace(chatBody, `false`, `true`, 1),
		strings.Replace(chatBody, `false`, `null`, 1), strings.Replace(chatBody, `false`, `0`, 1),
		strings.Replace(chatBody, `"model":`, `"other":`, 1),
		strings.Replace(chatBody, `"stream":false`, `"stream":false,"str\u0065am":false`, 1),
		strings.Replace(chatBody, `proof`, string([]byte{0xff}), 1),
		strings.Replace(chatBody, `"stream":false`, `"stream":false,"stream":false`, 1),
		strings.Replace(chatBody, `"model":`, `"model":"other","model":`, 1),
		strings.Replace(chatBody, `s1-proof-model`, `other`, 1),
		strings.Replace(chatBody, `"role":"user"`, `"role":"user","role":"assistant"`, 1),
		strings.Replace(chatBody, `"stream"`, `"Stream"`, 1),
		strings.Replace(chatBody, `"messages":`, `"me\u017f\u017fages":[],"messages":`, 1),
		strings.Replace(chatBody, `"proof"`, `"\ud800"`, 1),
		strings.Repeat(" ", maxChatBytes) + chatBody,
	} {
		r := httptest.NewRequest("POST", chatPath, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 73))
		if _, err := readChat(r); err == nil {
			t.Fatalf("accepted ambiguous body %.100s", body)
		}
	}
	for _, target := range []string{chatPath + "?", chatPath + "?x=1", "/v1/%63hat/completions", "//v1/chat/completions", "/v1/chat/../chat/completions", "http://example.test" + chatPath} {
		r := httptest.NewRequest("POST", target, strings.NewReader(chatBody))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 73))
		if _, err := readChat(r); err == nil {
			t.Fatalf("accepted target %s", target)
		}
	}
	r := httptest.NewRequest("POST", chatPath, strings.NewReader(chatBody))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 73))
	got, err := readChat(r)
	if err != nil || !bytes.Equal(got, []byte(chatBody)) {
		t.Fatal("valid body changed", err)
	}
	for _, header := range []string{"Content-Encoding", "Upgrade", "Expect", "X-API-Key"} {
		clone := r.Clone(r.Context())
		clone.Header.Set(header, "invalid")
		clone.Body = io.NopCloser(strings.NewReader(chatBody))
		if _, err := readChat(clone); err == nil {
			t.Fatal("accepted", header)
		}
	}
}

func TestPrivateChatReportsActualAttemptWithoutRetry(t *testing.T) {
	var hits atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.Copy(io.Discard, r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer gateway.Close()
	var expired atomic.Bool
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v chatVerdict
		json.NewDecoder(r.Body).Decode(&v)
		v.ExpiresAtMS = time.Now().Add(500 * time.Millisecond).UnixMilli()
		if expired.Load() {
			v.ExpiresAtMS = time.Now().UnixMilli() - 1
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}))
	defer manager.Close()
	h := newPrivateChat(manager.URL, gateway.URL)
	call := func() int {
		r := httptest.NewRequest("POST", chatPath, strings.NewReader(chatBody))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 73))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if got := call(); got != 502 || hits.Load() != 1 {
		t.Fatal("actual CPA attempt relabeled or replayed", got, hits.Load())
	}
	expired.Store(true)
	if got := call(); got != 503 || hits.Load() != 1 {
		t.Fatal("expired verdict forwarded", got, hits.Load())
	}
}

func TestPrivateChatFreshVerdictForwardingAndNoReuse(t *testing.T) {
	var hits, checks atomic.Int32
	var fail atomic.Bool
	var old chatVerdict
	var mu sync.Mutex
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks.Add(1)
		var v chatVerdict
		json.NewDecoder(r.Body).Decode(&v)
		mu.Lock()
		defer mu.Unlock()
		if fail.Load() {
			v = old
		} else {
			v.ExpiresAtMS = time.Now().Add(500 * time.Millisecond).UnixMilli()
			old = v
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}))
	defer manager.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		if string(b) != chatBody || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 73) || r.Header.Get("X-Secret") != "" {
			t.Error("changed request or leaked header")
		}
		w.WriteHeader(200)
	}))
	defer gateway.Close()
	h := newPrivateChat(manager.URL, gateway.URL)
	call := func() int {
		r := httptest.NewRequest("POST", chatPath, strings.NewReader(chatBody))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 73))
		r.Header.Set("X-Secret", "private")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if call() != 200 || call() != 200 || hits.Load() != 2 || checks.Load() != 2 {
		t.Fatal("not per request")
	}
	fail.Store(true)
	if call() != 503 || hits.Load() != 2 {
		t.Fatal("reused verdict")
	}
	// Restarting the consumer still creates a different request binding.
	h = newPrivateChat(manager.URL, gateway.URL)
	if call() != 503 || hits.Load() != 2 {
		t.Fatal("restart reused verdict")
	}
	fail.Store(false)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if call() != 200 {
				t.Error("concurrent request failed")
			}
		}()
	}
	wg.Wait()
	if hits.Load() != 10 {
		t.Fatal(hits.Load())
	}
}

func TestPrivateVerdictNamespaceDeniedFreshLegacyAndRestart(t *testing.T) {
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	for _, fresh := range []bool{false, true, true} {
		var h *Handler
		if fresh {
			h = NewFreshHandler(&FreshRoutes{}, u, u, log.New(io.Discard, "", 0), nil)
		} else {
			h = NewHandler(NewTransitionalClassifier(), u, u, log.New(io.Discard, "", 0))
		}
		for _, p := range []string{PrivateChatAdmissionPath, PrivateChatAdmissionPath + "/", "/internal/%66resh-chat-admission", "/internal%2ffresh-chat-admission", "/internal%252ffresh-chat-admission", "/x/../internal/fresh-chat-admission", "/internal\\fresh-chat-admission", "/control/../internal/fresh-chat-admission"} {
			r := httptest.NewRequest("POST", p, strings.NewReader(`{}`))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 404 {
				t.Fatal(p, w.Code)
			}
		}
	}
	if hits.Load() != 0 {
		t.Fatal("private path proxied")
	}
}

func TestVerdictExpiresAfterDialBeforeFirstByte(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	var wrote atomic.Bool
	c := &admittedConn{Conn: left, until: time.Now().Add(-time.Millisecond).UnixMilli(), wrote: &wrote}
	if n, err := c.Write([]byte("POST")); n != 0 || err == nil || wrote.Load() {
		t.Fatal("expired verdict wrote CPA bytes")
	}
}
