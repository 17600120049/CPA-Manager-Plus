package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	keyrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshkeys"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacypreflight"
)

// Exercise the production Fresh handler in a distinct process with genuine
// TCP peer addresses. This does not qualify a full Docker/runtime deployment.
func TestPrivateStagedKeyProofRealProcessLoopback(t *testing.T) {
	paths := legacypreflight.DefaultPaths(filepath.Join(t.TempDir(), "manager.sqlite"))
	paths.Config = filepath.Join(filepath.Dir(paths.Database), "config.json")
	opened, err := fresh.Open(t.Context(), paths, fresh.OpenOptions{Initialize: true, AdminKey: strategyTestAdmin})
	if err != nil {
		t.Fatal(err)
	}
	service := freshkeys.New(keyrepo.New(opened.DB), opened.Protector)
	result, created, err := service.Create(t.Context(), strings.Repeat("d", 32))
	if err != nil || !created {
		_ = opened.Close()
		t.Fatalf("create staged credential: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		enabled bool
	}{
		{name: "default-disabled"},
		{name: "explicit-opt-in", enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			t.Cleanup(cancel)
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPrivateStagedKeyProofListenerChild$")
			optIn := "0"
			if tc.enabled {
				optIn = "1"
			}
			cmd.Env = append(os.Environ(), "CPAMP_TEST_FRESH_KEY_PROOF_DB="+paths.Database, "CPAMP_PRIVATE_CLIENT_AUTH_PROOF="+optIn)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			})
			reader := bufio.NewScanner(stdout)
			if !reader.Scan() || !strings.HasPrefix(reader.Text(), "READY ") {
				t.Fatalf("Fresh child did not start: line=%q error=%v stderr=%s", reader.Text(), reader.Err(), stderr.String())
			}
			baseURL := "http://" + strings.TrimPrefix(reader.Text(), "READY ")
			client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
			t.Cleanup(client.CloseIdleConnections)
			call := func(method, path, authorization string, want int) string {
				t.Helper()
				req, err := http.NewRequestWithContext(ctx, method, baseURL+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if authorization != "" {
					req.Header.Set("Authorization", authorization)
				}
				response, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != want || response.Header.Get("Cache-Control") != "no-store" || strings.Contains(string(body), result.CreatedSecret) {
					t.Fatalf("private TCP probe %s %s status=%d want=%d body=%q", method, path, response.StatusCode, want, body)
				}
				return string(body)
			}
			path := privateStagedKeyProofPath
			valid := "Bearer " + result.CreatedSecret
			if !tc.enabled {
				call(http.MethodPost, path, valid, http.StatusNotFound)
				return
			}
			call(http.MethodPost, path, "", http.StatusUnauthorized)
			call(http.MethodPost, path, "Bearer incorrect", http.StatusUnauthorized)
			call(http.MethodGet, path, valid, http.StatusMethodNotAllowed)
			call(http.MethodPost, path+"?unexpected=1", valid, http.StatusNotFound)
			call(http.MethodPost, "/internal/fresh-client-key-stage/%70robe", valid, http.StatusNotFound)
			body := call(http.MethodPost, path, valid, http.StatusOK)
			if !strings.Contains(body, `"managerStagedKeyMatched":true`) ||
				!strings.Contains(body, `"runtimeEffective":false`) ||
				!strings.Contains(body, `"gatewayAuthorized":false`) {
				t.Fatalf("live Manager staged match represented Runtime authorization: %q", body)
			}
		})
	}
}

// This helper runs only when invoked by the parent test. net/http provides the
// actual RemoteAddr of the loopback TCP connection.
func TestPrivateStagedKeyProofListenerChild(t *testing.T) {
	database := os.Getenv("CPAMP_TEST_FRESH_KEY_PROOF_DB")
	if database == "" {
		return
	}
	paths := legacypreflight.DefaultPaths(database)
	paths.Config = filepath.Join(filepath.Dir(database), "config.json")
	opened, err := fresh.Open(context.Background(), paths, fresh.OpenOptions{AdminKey: strategyTestAdmin})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	fmt.Fprintf(os.Stdout, "READY %s\n", listener.Addr().String())
	if err := http.Serve(listener, newFreshApplication(opened, config.Config{}, nil)); err != nil {
		t.Fatal(err)
	}
}
