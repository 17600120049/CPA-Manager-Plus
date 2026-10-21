package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/ingress/internal/ingress"
)

const (
	defaultAddr       = "0.0.0.0:18317"
	defaultManagerURL = "http://cpamp-manager:18317"
	defaultGatewayURL = "http://cpamp-runtime:8317"
)

type config struct {
	addr        string
	managerURL  *url.URL
	gatewayURL  *url.URL
	controlAddr string
	pahS0     bool
}

func main() {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		log.Fatalf("configure CPAMP Ingress: %v", err)
	}
	logger := log.New(os.Stderr, "cpamp-ingress: ", log.LstdFlags)
	fresh := &ingress.FreshRoutes{}
	authority := managerFreshAuthority(cfg.managerURL)
	handler := ingress.NewFreshHandler(fresh, cfg.managerURL, cfg.gatewayURL, logger, managerLegacyGateway(authority))
	if cfg.pahS0 {
		handler.EnablePAHS0(managerPAHS0Guard(cfg.managerURL))
	}
	s2a, s2b := os.Getenv("CPAMP_PRIVATE_S2A_CHAT") == "1", os.Getenv("CPAMP_PRIVATE_S2B_CHAT") == "1"
	if s2a && s2b {
		logger.Fatal("conflicting private chat profiles")
	}
	if s2b && !handler.EnablePrivateS2BChat(cfg.managerURL, cfg.gatewayURL, os.Getenv("CPAMP_PRIVATE_S2B_MODEL")) {
		logger.Fatal("invalid private S2-B startup fixture")
	}
	if s2a {
		handler.EnablePrivateChat(cfg.managerURL, cfg.gatewayURL)
	}
	control := &http.Server{Addr: cfg.controlAddr, Handler: fresh.ControlHandler(authority), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 1 << 16}
	server := &http.Server{
		Addr:              cfg.addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result := make(chan error, 2)
	go func() { result <- control.ListenAndServe() }()
	go func() {
		logger.Printf("listening on %s", cfg.addr)
		result <- server.ListenAndServe()
	}()

	select {
	case err := <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("serve: %v", err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Fatalf("shutdown: %v", err)
		}
		_ = control.Shutdown(shutdownCtx)
	}
}

func loadConfig(getenv func(string) string) (config, error) {
	addr := strings.TrimSpace(getenv("CPAMP_INGRESS_ADDR"))
	if addr == "" {
		addr = defaultAddr
	}
	managerURL, err := parseUpstream("CPAMP_MANAGER_URL", getenv("CPAMP_MANAGER_URL"), defaultManagerURL)
	if err != nil {
		return config{}, err
	}
	gatewayURL, err := parseUpstream("CPAMP_GATEWAY_URL", getenv("CPAMP_GATEWAY_URL"), defaultGatewayURL)
	if err != nil {
		return config{}, err
	}
	controlAddr := strings.TrimSpace(getenv("CPAMP_INGRESS_CONTROL_ADDR"))
	if controlAddr == "" {
		controlAddr = "0.0.0.0:9082"
	}
	flag := strings.TrimSpace(getenv("CPAMP_PRIVATE_PAH_S0"))
	build := strings.TrimSpace(getenv("CPAMP_PAH_S0_BUILD"))
	if (flag != "" && flag != "0" && flag != "1") ||
		(build != "" && build != "0" && build != "1") {
		return config{}, errors.New("PAH-S0 build and runtime flags must be 0 or 1")
	}
	if (flag == "1") != (build == "1") {
		return config{}, errors.New("PAH-S0 Ingress runtime does not match its image build")
	}
	if flag == "1" {
		if managerURL.Hostname() != "127.0.0.1" || gatewayURL.Hostname() != "127.0.0.1" {
			return config{}, errors.New("PAH-S0 requires loopback Manager and CPA upstreams")
		}
		if !strings.HasPrefix(controlAddr, "127.0.0.1:") {
			return config{}, errors.New("PAH-S0 requires loopback Ingress control listener")
		}
	}
	return config{addr: addr, managerURL: managerURL, gatewayURL: gatewayURL, controlAddr: controlAddr, pahS0: flag == "1"}, nil
}

var errLegacyManager = errors.New("Manager is serving the legacy application")

// Only the fully started legacy Manager serves 204 on the existing private
// authority endpoint. Unknown/Fresh/startup/error responses never open Gateway.
// Retain this process-local mode observation for legacy Manager-outage continuity;
// an Ingress restart must obtain it again. Fresh admission cannot replace an
// initialized legacy store through normal startup.
func managerLegacyGateway(authority func(context.Context) (ingress.FreshRouteState, error)) func(context.Context) bool {
	var confirmed atomic.Bool
	return func(ctx context.Context) bool {
		if !confirmed.Load() {
			_, err := authority(ctx)
			if errors.Is(err, errLegacyManager) {
				confirmed.Store(true)
			}
		}
		return confirmed.Load()
	}
}

func managerFreshAuthority(managerURL *url.URL) func(context.Context) (ingress.FreshRouteState, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	endpoint := *managerURL
	endpoint.Path = ingress.FreshRoutingAuthorityPath
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return func(ctx context.Context) (ingress.FreshRouteState, error) {
		var state ingress.FreshRouteState
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return state, err
		}
		req.Header.Set("Cache-Control", "no-store")
		res, err := client.Do(req)
		if err != nil {
			return state, err
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusNoContent {
			return state, errLegacyManager
		}
		if res.StatusCode != http.StatusOK {
			return state, fmt.Errorf("Manager routing authority unavailable: status %d", res.StatusCode)
		}
		decoder := json.NewDecoder(io.LimitReader(res.Body, 8192))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&state); err != nil {
			return ingress.FreshRouteState{}, err
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			return ingress.FreshRouteState{}, errors.New("Manager routing authority returned trailing data")
		}
		return state, nil
	}
}

func parseUpstream(name, value, fallback string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%s must be an http origin without credentials, path, query, or fragment", name)
	}
	parsed.Path = ""
	return parsed, nil
}

// managerPAHS0Guard is an uncached private readiness probe; 404, redirects,
// connection failure, missing header and Manager restarts all deny the request.
func managerPAHS0Guard(managerURL *url.URL) func(context.Context) bool {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	endpoint := *managerURL
	endpoint.Path = "/internal/pah-s0/resource-guard"
	endpoint.RawPath, endpoint.RawQuery, endpoint.Fragment = "", "", ""
	return func(ctx context.Context) bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return false
		}
		req.Header.Set("Cache-Control", "no-store")
		res, err := client.Do(req)
		if err != nil {
			return false
		}
		defer res.Body.Close()
		return res.StatusCode == http.StatusNoContent && res.Header.Get("Cache-Control") == "no-store"
	}
}
