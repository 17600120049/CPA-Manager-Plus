package ingress

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestS2BModelUsesSameStrictRequestParser(t *testing.T) {
	manager, _ := url.Parse("http://127.0.0.1:18319")
	gateway, _ := url.Parse("http://127.0.0.1:8317")
	h := &Handler{}
	if !h.EnablePrivateS2BChat(manager, gateway, "unit-model") {
		t.Fatal("private startup rejected")
	}
	b := strings.Replace(chatBody, "s1-proof-model", "unit-model", 1)
	for _, body := range []string{b, chatBody, strings.Replace(b, `"stream":false`, `"stream":true`, 1), strings.Replace(b, `"model":`, `"model":"other","model":`, 1), b + `{}`} {
		r := httptest.NewRequest("POST", chatPath, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 73))
		got, err := readChatModel(r, h.chat.model)
		if body == b {
			if err != nil || !bytes.Equal(got, []byte(b)) {
				t.Fatal("checked bytes changed")
			}
		} else if err == nil {
			t.Fatal("unapproved request accepted")
		}
	}
	for _, model := range []string{"", "s1-proof-model", "*", "unit-model?x", "unit-model\n"} {
		if (&Handler{}).EnablePrivateS2BChat(manager, gateway, model) {
			t.Fatal("invalid startup model")
		}
	}
	other, _ := url.Parse("http://localhost:8317")
	if (&Handler{}).EnablePrivateS2BChat(manager, other, "unit-model") {
		t.Fatal("non-pinned origin")
	}
}

func TestS2BRejectsConflictingManagerModelBeforeCPA(t *testing.T) {
	var hits atomic.Int32
	var model atomic.Value
	model.Store("unit-model")
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v chatVerdict
		json.NewDecoder(r.Body).Decode(&v)
		v.Model = model.Load().(string)
		v.ExpiresAtMS = time.Now().Add(500 * time.Millisecond).UnixMilli()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}))
	defer manager.Close()
	cpa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(200) }))
	defer cpa.Close()
	h := newPrivateChat(manager.URL, cpa.URL)
	h.model = "unit-model"
	for _, allowed := range []string{"unit-model", "other-model", "", "s1-proof-model"} {
		model.Store(allowed)
		r := httptest.NewRequest("POST", chatPath, strings.NewReader(strings.Replace(chatBody, "s1-proof-model", "unit-model", 1)))
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 73))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 503
		if allowed == "unit-model" {
			want = 200
		}
		if w.Code != want {
			t.Fatal("conflicting model projection accepted", w.Code)
		}
	}
	if hits.Load() != 1 {
		t.Fatal("conflicting model reached CPA", hits.Load())
	}
}
