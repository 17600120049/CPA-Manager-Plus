package protocol

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/artifact"
	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/journal"
	"github.com/seakee/cpa-manager-plus/apps/runtime-supervisor/internal/lifecycle"
)

type destinationFake struct {
	calls int
	err   error
	last  lifecycle.DestinationRequest
}

func (f *destinationFake) StageDestination(_ context.Context, r lifecycle.DestinationRequest) (journal.Operation, error) {
	f.calls++
	f.last = r
	if f.err != nil {
		return journal.Operation{}, f.err
	}
	return journal.Operation{OperationID: r.OperationID, OperationType: "stage_destination", RuntimeIdentity: r.ExpectedRuntimeIdentity, RuntimeGeneration: r.ExpectedRuntimeGeneration, State: journal.StateSucceeded}, nil
}

func TestDestinationProtocolReportsGatewayAuthRefusalForNewManaged(t *testing.T) {
	fake := &destinationFake{err: lifecycle.ErrGatewayAuthPresent}
	h, err := NewHandler(Config{RuntimeIdentity: "runtime-01", RuntimeGeneration: 1, Token: "test-runtime-token", Destination: fake})
	if err != nil {
		t.Fatal(err)
	}
	r := lifecycle.DestinationRequest{OperationID: "fresh:managed-destination", ExpectedRuntimeIdentity: "runtime-01", ExpectedRuntimeGeneration: 1, ExpectedActiveArtifactID: artifact.ID("sha256:" + strings.Repeat("a", 64)), SourceIdentity: "managed:sha256:" + strings.Repeat("b", 64), SourceClass: lifecycle.SourceClassNewManaged, APIKeys: []string{"sk-" + strings.Repeat("d", 64)}, ManagementKey: strings.Repeat("c", 64)}
	body, _ := json.Marshal(r)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", destinationPath, strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer test-runtime-token")
	h.ServeHTTP(recorder, request)
	if recorder.Code != 409 || !strings.Contains(recorder.Body.String(), `"gateway_auth_present"`) || strings.Contains(recorder.Body.String(), r.ManagementKey) || fake.calls != 1 {
		t.Fatalf("gateway auth refusal status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDestinationProtocolIsPrivateBoundedTypedAndSecretFree(t *testing.T) {
	fake := &destinationFake{}
	h, err := NewHandler(Config{RuntimeIdentity: "runtime-01", RuntimeGeneration: 1, Token: "test-runtime-token", Destination: fake})
	if err != nil {
		t.Fatal(err)
	}
	r := lifecycle.DestinationRequest{OperationID: "adopt-destination", ExpectedRuntimeIdentity: "runtime-01", ExpectedRuntimeGeneration: 1, ExpectedActiveArtifactID: artifact.ID("sha256:" + strings.Repeat("a", 64)), SourceIdentity: "external:sha256:" + strings.Repeat("b", 64), SourceClass: "network_existing", APIKeys: []string{"private-client-secret"}, ManagementKey: strings.Repeat("c", 64)}
	body, _ := json.Marshal(r)
	for _, test := range []struct {
		method, token, body string
		want                int
	}{
		{"POST", "", string(body), 401},
		{"GET", "test-runtime-token", string(body), 405},
		{"POST", "test-runtime-token", strings.Replace(string(body), `"operationId":`, `"operationId":"duplicate","operationId":`, 1), 400},
		{"POST", "test-runtime-token", strings.Replace(string(body), `"operationId":`, `"OperationID":`, 1), 400},
		{"POST", "test-runtime-token", strings.TrimSuffix(string(body), "}") + `,"configPath":"/source/config"}`, 400},
		{"POST", "test-runtime-token", string(body) + ` {}`, 400},
		{"POST", "test-runtime-token", string(body), 200},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, destinationPath, strings.NewReader(test.body))
		if test.token != "" {
			request.Header.Set("Authorization", "Bearer "+test.token)
		}
		h.ServeHTTP(recorder, request)
		if recorder.Code != test.want {
			t.Fatalf("protocol status=%d want=%d", recorder.Code, test.want)
		}
		if strings.Contains(recorder.Body.String(), r.APIKeys[0]) || strings.Contains(recorder.Body.String(), r.ManagementKey) {
			t.Fatal("destination secrets escaped response")
		}
	}
	if fake.calls != 1 {
		t.Fatal("invalid/private request reached executor")
	}
}

// The Fresh wizard's plugin opt-in travels on the destination request. It is a
// product decision, not a path, so it is part of the closed key set the decoder
// admits; before it was named here every opted-in install was refused as
// invalid_request and could never reach the Supervisor template.
func TestDestinationProtocolCarriesPluginOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		fake := &destinationFake{}
		h, err := NewHandler(Config{RuntimeIdentity: "runtime-01", RuntimeGeneration: 1, Token: "test-runtime-token", Destination: fake})
		if err != nil {
			t.Fatal(err)
		}
		r := lifecycle.DestinationRequest{OperationID: "fresh:managed-destination", ExpectedRuntimeIdentity: "runtime-01", ExpectedRuntimeGeneration: 1, ExpectedActiveArtifactID: artifact.ID("sha256:" + strings.Repeat("a", 64)), SourceIdentity: "managed:sha256:" + strings.Repeat("b", 64), SourceClass: lifecycle.SourceClassNewManaged, APIKeys: []string{"sk-" + strings.Repeat("aB3", 21) + "Z"}, ManagementKey: strings.Repeat("c", 64), PluginsEnabled: enabled}
		body, _ := json.Marshal(r)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", destinationPath, strings.NewReader(string(body)))
		request.Header.Set("Authorization", "Bearer test-runtime-token")
		h.ServeHTTP(recorder, request)
		if recorder.Code != 200 || fake.calls != 1 {
			t.Fatalf("pluginsEnabled=%v status=%d body=%s", enabled, recorder.Code, recorder.Body.String())
		}
		if fake.last.PluginsEnabled != enabled {
			t.Fatalf("pluginsEnabled=%v reached the executor as %v", enabled, fake.last.PluginsEnabled)
		}
	}
}

// The opt-in is still a boolean, never a directory: a caller cannot direct
// plugin discovery at a path of its choosing.
func TestDestinationProtocolRefusesPluginDirectory(t *testing.T) {
	fake := &destinationFake{}
	h, err := NewHandler(Config{RuntimeIdentity: "runtime-01", RuntimeGeneration: 1, Token: "test-runtime-token", Destination: fake})
	if err != nil {
		t.Fatal(err)
	}
	r := lifecycle.DestinationRequest{OperationID: "fresh:managed-destination", ExpectedRuntimeIdentity: "runtime-01", ExpectedRuntimeGeneration: 1, ExpectedActiveArtifactID: artifact.ID("sha256:" + strings.Repeat("a", 64)), SourceIdentity: "managed:sha256:" + strings.Repeat("b", 64), SourceClass: lifecycle.SourceClassNewManaged, APIKeys: []string{"sk-" + strings.Repeat("aB3", 21) + "Z"}, ManagementKey: strings.Repeat("c", 64), PluginsEnabled: true}
	body, _ := json.Marshal(r)
	hostile := strings.TrimSuffix(string(body), "}") + `,"pluginsDir":"/tmp"}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", destinationPath, strings.NewReader(hostile))
	request.Header.Set("Authorization", "Bearer test-runtime-token")
	h.ServeHTTP(recorder, request)
	if recorder.Code != 400 || fake.calls != 0 {
		t.Fatalf("plugin directory status=%d calls=%d", recorder.Code, fake.calls)
	}
}
