package cpa

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReadConfigDocumentKeepsOnlyTheDocument proves the read is one bounded GET
// and that presence, not truthiness, distinguishes missing from null.
func TestReadConfigDocumentKeepsOnlyTheDocument(t *testing.T) {
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v8/management/config" {
			t.Error("unexpected read", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer management-test-only" {
			t.Error("management credential missing")
		}
		gets++
		_, _ = w.Write([]byte(`{"routing":{"strategy":"round-robin","session-affinity":null},"access":{"api-keys":["sk-secret"]}}`))
	}))
	defer server.Close()
	tr := &Transport{client: server.Client(), endpoint: server.URL}
	document, err := tr.ReadConfigDocument(t.Context(), "management-test-only")
	if err != nil || gets != 1 {
		t.Fatalf("document=%v err=%v gets=%d", document != nil, err, gets)
	}
	if _, presence := LookupConfigValue(document, "routing.strategy"); presence != ConfigPresent {
		t.Fatal(presence)
	}
	if _, presence := LookupConfigValue(document, "routing.session-affinity"); presence != ConfigNull {
		t.Fatal(presence)
	}
	if _, presence := LookupConfigValue(document, "routing.retry.request-retry"); presence != ConfigMissing {
		t.Fatal(presence)
	}
	if _, presence := LookupConfigValue(document, "routing.strategy.detail"); presence != ConfigMissing {
		t.Fatal(presence)
	}
	if leaf, presence := LookupConfigValue(document, "access.api-keys"); presence != ConfigPresent || leaf == nil {
		t.Fatal("document read changed the projection")
	}
	if _, err = tr.ReadConfigDocument(t.Context(), ""); !errors.Is(err, ErrProofUnavailable) {
		t.Fatal("read without a credential accepted")
	}
}

// TestConfigWritesUseOneExactEndpoint pins the wire contract of a group save:
// one PATCH below the group prefix, one DELETE per restored leaf and one PUT per
// rule family, each with the management credential.
func TestConfigWritesUseOneExactEndpoint(t *testing.T) {
	type call struct {
		method      string
		path        string
		contentType string
		body        string
	}
	var calls []call
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, call{r.Method, r.URL.Path, r.Header.Get("Content-Type"), string(body)})
		w.WriteHeader(status)
	}))
	defer server.Close()
	tr := &Transport{client: server.Client(), endpoint: server.URL}

	if err := tr.PatchConfigPath(t.Context(), "management-test-only", "routing.retry", []byte(`{"request-retry":3}`)); err != nil {
		t.Fatal(err)
	}
	if err := tr.DeleteConfigPath(t.Context(), "management-test-only", "requests.proxy-url"); err != nil {
		t.Fatal(err)
	}
	if err := tr.PutConfigValue(t.Context(), "management-test-only", "requests.payload.filter", []byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	want := []call{
		{http.MethodPatch, "/v8/management/config/routing/retry", "application/json", `{"request-retry":3}`},
		{http.MethodDelete, "/v8/management/config/requests/proxy-url", "", ""},
		{http.MethodPut, "/v8/management/config/requests/payload/filter", "application/json", `[]`},
	}
	if len(calls) != len(want) {
		t.Fatalf("calls %+v", calls)
	}
	for index, expected := range want {
		if calls[index] != expected {
			t.Fatalf("call %d = %+v, want %+v", index, calls[index], expected)
		}
	}
	if err := tr.PatchConfigPath(t.Context(), "", "routing", []byte(`{}`)); !errors.Is(err, ErrProofUnavailable) {
		t.Fatal("write without a credential accepted")
	}
}

// TestConfigWriteRejectionsSeparateDefiniteFromUnknown proves CPA's own verdict
// is mapped without echoing a quoted upstream value.
func TestConfigWriteRejectionsSeparateDefiniteFromUnknown(t *testing.T) {
	for _, tc := range []struct {
		status   int
		body     string
		definite bool
		code     string
		field    string
		message  string
	}{
		{http.StatusBadRequest, `{"error":"invalid_path"}`, true, "invalid_path", "", ""},
		{http.StatusBadRequest, `{"error":"read_only_field","field":"credentials/concurrency/lifecycle-config-revision"}`, true, "read_only_field", "credentials/concurrency/lifecycle-config-revision", ""},
		{http.StatusUnprocessableEntity, `{"error":"invalid_config","message":"yaml: unmarshal errors:\n  line 3: cannot unmarshal !!str ` + "`sk-live-secret`" + ` into int"}`, true, "invalid_config", "", "yaml: unmarshal errors: line 3: cannot unmarshal !!str into int"},
		{http.StatusNotFound, ``, true, "not_found", "", ""},
		{http.StatusInternalServerError, `{"error":"write_failed"}`, false, "invalid_config", "", ""},
		{http.StatusTooManyRequests, ``, false, "invalid_config", "", ""},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		tr := &Transport{client: server.Client(), endpoint: server.URL}
		err := tr.PatchConfigPath(t.Context(), "management-test-only", "routing", []byte(`{"strategy":"round-robin"}`))
		server.Close()
		if tc.definite {
			var rejection ConfigWriteError
			if !errors.As(err, &rejection) {
				t.Fatalf("status %d: %v", tc.status, err)
			}
			if rejection.Code != tc.code || rejection.Field != tc.field || rejection.Message != tc.message {
				t.Fatalf("status %d: %+v", tc.status, rejection)
			}
			if strings.Contains(rejection.Message, "sk-live-secret") {
				t.Fatal("upstream value leaked into the diagnostic")
			}
			continue
		}
		if !errors.Is(err, ErrApplyUnknown) {
			t.Fatalf("status %d: %v", tc.status, err)
		}
	}
}

// TestConfigWriteTransportFailureIsUnknown covers a lost response: the caller
// must treat it as an unknown result and read back, never resend.
func TestConfigWriteTransportFailureIsUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijack, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = hijack.Close()
		}
	}))
	defer server.Close()
	tr := &Transport{client: server.Client(), endpoint: server.URL}
	if err := tr.PatchConfigPath(context.Background(), "management-test-only", "routing", []byte(`{}`)); !errors.Is(err, ErrApplyUnknown) {
		t.Fatalf("lost reply not unknown: %v", err)
	}
}
