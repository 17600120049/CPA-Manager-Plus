package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

type quotaCooldownRecoveryStub struct {
	cooldownID   int64
	updatedAtMS  int64
	authFileName string
	authIndex    string
	provider     string
	recovered    bool
	err          error
}

func (s *quotaCooldownRecoveryStub) RecoverOwnedCooldown(
	_ context.Context,
	authFileName string,
	authIndex string,
	provider string,
	cooldownID int64,
	updatedAtMS int64,
) (bool, error) {
	s.authFileName = authFileName
	s.authIndex = authIndex
	s.provider = provider
	s.cooldownID = cooldownID
	s.updatedAtMS = updatedAtMS
	return s.recovered, s.err
}

func TestServerCompatQuotaCooldownsList(t *testing.T) {
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	manager := collector.NewManager(cfg, db)
	handler := New(cfg, db, manager).Handler()

	now := int64(1_700_000_000_000)
	persisted, err := db.QuotaCooldowns.UpsertActive(context.Background(), model.QuotaCooldownUpsert{
		AuthFileName:    "xai-1.json",
		AuthIndex:       "0",
		Provider:        "xai",
		Owner:           model.QuotaCooldownOwnerXAIFreeUsage,
		RecoverAtMS:     now + 3_600_000,
		DisabledAtMS:    now,
		AccountSnapshot: "xai-user@example.com",
		EventHash:       "should-not-leak",
		EvidenceJSON:    `{"provider":"xai","kind":"included_free_usage","state":"exhausted","code":"subscription:free-usage-exhausted","model":"Bearer sk-sensitive-token","unit":"tokens","actual":1024413,"limit":1000000,"remaining":0,"overage":24413,"window_kind":"rolling_24h","recover_at_ms":1700003600000,"recover_at_estimated":true,"source":"response_body"}`,
	})
	if err != nil {
		t.Fatalf("seed cooldown: %v", err)
	}
	if persisted.RecoverAtMS != now+3_600_000 {
		t.Fatalf("seed recoverAtMs = %d", persisted.RecoverAtMS)
	}

	rr := testutil.Request(t, handler, http.MethodGet, "/usage-service/quota-cooldowns", "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, http.StatusOK)

	var resp struct {
		Items []struct {
			CooldownID      int64  `json:"cooldownId"`
			UpdatedAtMs     int64  `json:"updatedAtMs"`
			AuthFileName    string `json:"authFileName"`
			AuthIndex       string `json:"authIndex"`
			AccountSnapshot string `json:"accountSnapshot"`
			Provider        string `json:"provider"`
			Owner           string `json:"owner"`
			RecoverAtMs     int64  `json:"recoverAtMs"`
			DisabledAtMs    int64  `json:"disabledAtMs"`
			CreatedAtMs     int64  `json:"createdAtMs"`
			Evidence        struct {
				Provider           string `json:"provider"`
				Actual             int64  `json:"actual"`
				Limit              int64  `json:"limit"`
				Remaining          int64  `json:"remaining"`
				Overage            int64  `json:"overage"`
				RecoverAtEstimated bool   `json:"recover_at_estimated"`
			} `json:"evidence"`
		} `json:"items"`
	}
	testutil.DecodeJSON(t, rr, &resp)
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1, body = %s", len(resp.Items), rr.Body.String())
	}
	item := resp.Items[0]
	if item.AuthFileName != "xai-1.json" || item.AccountSnapshot != "xai-user@example.com" || item.Provider != "xai" || item.Owner != model.QuotaCooldownOwnerXAIFreeUsage {
		t.Fatalf("item = %#v", item)
	}
	if item.RecoverAtMs != now+3_600_000 || item.DisabledAtMs != now || item.CreatedAtMs <= 0 || item.CooldownID != persisted.ID || item.UpdatedAtMs != persisted.UpdatedAtMS {
		t.Fatalf("timestamps = %#v", item)
	}
	if item.Evidence.Provider != "xai" || item.Evidence.Actual != 1_024_413 || item.Evidence.Limit != 1_000_000 || item.Evidence.Remaining != 0 || item.Evidence.Overage != 24_413 || !item.Evidence.RecoverAtEstimated {
		t.Fatalf("evidence = %#v", item.Evidence)
	}
	if body := rr.Body.String(); strings.Contains(body, "sk-sensitive-token") || !strings.Contains(body, "[redacted]") {
		t.Fatalf("response evidence was not sanitized, body = %s", body)
	}
	// The read-only view exposes only the account discriminator needed for exact
	// card matching; operational internals must remain private.
	if body := rr.Body.String(); containsInternalField(body) {
		t.Fatalf("response leaked internal fields, body = %s", body)
	}
}

func TestServerCompatQuotaCooldownRecoverUsesEventDrivenRecoveryService(t *testing.T) {
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	manager := collector.NewManager(cfg, db)
	server := New(cfg, db, manager)
	recovery := &quotaCooldownRecoveryStub{recovered: true}
	server.AppContext().QuotaCooldownRecoveryService = recovery

	rr := testutil.Request(
		t,
		server.Handler(),
		http.MethodPost,
		"/usage-service/quota-cooldowns/recover",
		`{"authFileName":"codex.json","authIndex":"auth-1","provider":"codex","cooldownId":1,"updatedAtMs":3000}`,
		testutil.AdminKey,
	)
	testutil.RequireStatus(t, rr, http.StatusOK)

	var resp struct {
		Recovered bool `json:"recovered"`
	}
	testutil.DecodeJSON(t, rr, &resp)
	if !resp.Recovered {
		t.Fatal("recovered = false, want true")
	}
	if recovery.authFileName != "codex.json" || recovery.authIndex != "auth-1" || recovery.provider != "codex" || recovery.cooldownID != 1 || recovery.updatedAtMS != 3000 {
		t.Fatalf("recovery request = %#v", recovery)
	}
}

func TestServerCompatQuotaCooldownRecoverRejectsMissingGeneration(t *testing.T) {
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	server := New(cfg, db, collector.NewManager(cfg, db))
	recovery := &quotaCooldownRecoveryStub{recovered: true}
	server.AppContext().QuotaCooldownRecoveryService = recovery
	for _, generation := range []string{"", `,"cooldownId":1`, `,"cooldownId":1,"updatedAtMs":0`} {
		rr := testutil.Request(t, server.Handler(), http.MethodPost, "/usage-service/quota-cooldowns/recover",
			`{"authFileName":"codex.json","authIndex":"auth-1","provider":"codex"`+generation+`}`, testutil.AdminKey)
		testutil.RequireStatus(t, rr, http.StatusBadRequest)
	}
	if recovery.authFileName != "" {
		t.Fatal("missing generation invoked mutation service")
	}
}

func TestServerCompatQuotaCooldownRecoverRequiresPanelAuth(t *testing.T) {
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	manager := collector.NewManager(cfg, db)
	server := New(cfg, db, manager)
	server.AppContext().QuotaCooldownRecoveryService = &quotaCooldownRecoveryStub{recovered: true}

	rr := testutil.Request(
		t,
		server.Handler(),
		http.MethodPost,
		"/usage-service/quota-cooldowns/recover",
		`{"authFileName":"codex.json","authIndex":"auth-1","provider":"codex"}`,
		"",
	)
	testutil.RequireStatus(t, rr, http.StatusUnauthorized)
}

func TestServerCompatQuotaCooldownsRequiresPanelAuth(t *testing.T) {
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	manager := collector.NewManager(cfg, db)
	handler := New(cfg, db, manager).Handler()

	noKey := testutil.Request(t, handler, http.MethodGet, "/usage-service/quota-cooldowns", "", "")
	testutil.RequireStatus(t, noKey, http.StatusUnauthorized)

	post := testutil.Request(t, handler, http.MethodPost, "/usage-service/quota-cooldowns", "", testutil.AdminKey)
	testutil.RequireStatus(t, post, http.StatusMethodNotAllowed)
}

func containsInternalField(body string) bool {
	for _, needle := range []string{"account_snapshot", "eventHash", "event_hash", "preDisabledState", "lastError"} {
		if strings.Contains(body, needle) {
			return true
		}
	}
	return false
}
