package monitoring

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/datamigration"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	usageservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/usage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

// These are local behavioral fixtures, never exact-source historical bundles.
// Lane D consumes their assertions; SRC-11/RET-01 still need source provenance.
type parityAccounting struct {
	Mode     string  `json:"cache_input_mode"`
	Uncached int64   `json:"uncached_input_tokens"`
	Input    int64   `json:"total_input_tokens"`
	Read     int64   `json:"cache_read_tokens"`
	Creation int64   `json:"cache_creation_tokens"`
	Total    int64   `json:"total_tokens"`
	Cost     float64 `json:"cost"`
}
type parityRecord struct {
	ID       string           `json:"id"`
	Release  string           `json:"minimum_semantics_release"`
	Input    json.RawMessage  `json:"input"`
	Expected parityAccounting `json:"expected"`
}
type parityContract struct {
	Schema  int              `json:"schema_version"`
	Price   store.ModelPrice `json:"price_per_million"`
	Records []parityRecord   `json:"records"`
}
type parityConsumerEvidence struct {
	ID         string           `json:"id"`
	Actual     parityAccounting `json:"actual"`
	Compatible json.RawMessage  `json:"compatible"`
}

func readParityContract(t *testing.T) parityContract {
	t.Helper()
	raw, err := os.ReadFile("../../../../../tests/fixtures/phase4a-behavioral-parity/contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract parityContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Schema != 1 || len(contract.Records) == 0 {
		t.Fatal("invalid parity contract")
	}
	return contract
}
func paritySourceEvidence(t *testing.T, db *sql.DB) []any {
	t.Helper()
	var hash, raw string
	var metadata, session sql.NullString
	var input, output, total int64
	var generate, stream sql.NullInt64
	err := db.QueryRow(`select event_hash, raw_json, response_metadata_json,
 session_id, input_tokens, output_tokens, total_tokens, generate, stream from usage_events`).
		Scan(&hash, &raw, &metadata, &session, &input, &output, &total, &generate, &stream)
	if err != nil {
		t.Fatal(err)
	}
	return []any{hash, raw, metadata, session, input, output, total, generate, stream}
}

func TestPhase4ABehavioralParityFixture(t *testing.T) {
	contract := readParityContract(t)
	ctx := context.Background()
	evidence := []parityConsumerEvidence{}
	for _, record := range contract.Records {
		t.Run(record.ID, func(t *testing.T) {
			if os.Getenv("CPAMP_PARITY_SOURCE_RELEASE") == "v1.14.4" && record.Release != "v1.14.4" {
				t.Skip("post-baseline compatibility delta")
			}
			event, err := usage.NormalizeRaw(record.Input)
			if err != nil {
				t.Fatal(err)
			}
			db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "parity.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			st := store.New(db)
			if err := st.SaveModelPrices(ctx, map[string]store.ModelPrice{event.Model: contract.Price}); err != nil {
				t.Fatal(err)
			}
			inserted, err := st.InsertEvents(ctx, []usage.Event{event, event})
			if err != nil || inserted.Inserted != 1 || inserted.Skipped != 1 {
				t.Fatalf("dedupe: %+v, %v", inserted, err)
			}
			sourceBefore := paritySourceEvidence(t, db)
			// Only the published Devin regression row is perturbed. The source
			// event must survive a bounded rediscovery/rebuild unchanged.
			if record.ID == "devin-838-read-mirror" {
				_, err = db.Exec(`update usage_events set cache_input_mode = 'fixture-stale',
    normalized_uncached_input_tokens = 999, normalized_total_input_tokens = 999,
    normalized_cache_read_tokens = 0, normalized_cache_creation_tokens = 0`)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(`insert into settings(key,value,updated_at_ms) values(?, '1', 1)
    on conflict(key) do update set value='1'`, datamigration.UsageCacheAccountingSemanticsRevisionKey); err != nil {
				t.Fatal(err)
			}
			state, err := st.DiscoverUsageCacheAccounting(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for pass := 0; state.Status != datamigration.StatusCompleted; pass++ {
				if pass >= 100 {
					t.Fatal("accounting migration did not complete")
				}
				batch, err := st.RunUsageCacheAccountingBatch(ctx, 1)
				if err != nil {
					t.Fatal(err)
				}
				state = batch.State
			}
			if after := paritySourceEvidence(t, db); !reflect.DeepEqual(sourceBefore, after) {
				t.Fatal("migration changed authoritative history")
			}
			var revision string
			if err := db.QueryRow("select value from settings where key=?", datamigration.UsageCacheAccountingSemanticsRevisionKey).Scan(&revision); err != nil {
				t.Fatal(err)
			}
			if revision != fmt.Sprint(datamigration.CurrentUsageCacheAccountingSemanticsRevision) {
				t.Fatalf("revision %s", revision)
			}
			actual := parityAccounting{}
			if err := db.QueryRow(`select cache_input_mode, normalized_uncached_input_tokens,
    normalized_total_input_tokens, normalized_cache_read_tokens,
    normalized_cache_creation_tokens, total_tokens from usage_events`).
				Scan(&actual.Mode, &actual.Uncached, &actual.Input, &actual.Read, &actual.Creation, &actual.Total); err != nil {
				t.Fatal(err)
			}
			response, err := New(st).Analytics(ctx, Request{FromMS: event.TimestampMS - 1, ToMS: event.TimestampMS + 1,
				Include: Include{Summary: true, EventsPage: &EventsPage{Limit: 10}}})
			if err != nil {
				t.Fatal(err)
			}
			if response.Events == nil || len(response.Events.Items) != 1 || response.Summary == nil {
				t.Fatal("missing production consumer")
			}
			actual.Cost = response.Summary.TotalCost
			item := response.Events.Items[0]
			if item.InputTokens != actual.Input || item.CacheReadTokens != actual.Read || item.CacheCreationTokens != actual.Creation || item.TotalTokens != actual.Total {
				t.Fatalf("Monitoring token mismatch: item=%+v derived=%+v", item, actual)
			}
			if item.SessionID != event.SessionID || item.ParentSessionID != event.ParentSessionID || item.ResponseModel != event.ResponseModel ||
				!reflect.DeepEqual(item.Generate, event.Generate) || !reflect.DeepEqual(item.Stream, event.Stream) ||
				!reflect.DeepEqual(item.ResponseMetadata, event.ResponseMetadata) {
				t.Fatal("Monitoring metadata mismatch")
			}
			if math.Abs(actual.Cost-record.Expected.Cost) > 1e-12 {
				t.Fatalf("cost actual %.12f, expected %.12f", actual.Cost, record.Expected.Cost)
			}
			tokensActual, tokensExpected := actual, record.Expected
			tokensActual.Cost, tokensExpected.Cost = 0, 0
			if !reflect.DeepEqual(tokensActual, tokensExpected) {
				t.Fatalf("actual %+v, expected %+v", actual, record.Expected)
			}
			exported, err := st.ExportJSONL(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(exported, []byte(`"raw_json"`)) || bytes.Contains(exported, []byte(`"fail_body"`)) {
				t.Fatal("export exposed private fields")
			}
			restored := newMonitoringTestStore(t)
			result, _, err := usageservice.New(restored).Import(ctx, bytes.NewReader(exported))
			if err != nil {
				t.Fatal(err)
			}
			_ = result
			restoredResponse, err := New(restored).Analytics(ctx, Request{FromMS: event.TimestampMS - 1, ToMS: event.TimestampMS + 1, Include: Include{EventsPage: &EventsPage{Limit: 10}}})
			if err != nil || restoredResponse.Events == nil || len(restoredResponse.Events.Items) != 1 {
				t.Fatalf("import consumer: %v", err)
			}
			restoredItem := restoredResponse.Events.Items[0]
			if restoredItem.EventHash != item.EventHash || restoredItem.InputTokens != item.InputTokens || restoredItem.TotalTokens != item.TotalTokens ||
				restoredItem.SessionID != item.SessionID || !reflect.DeepEqual(restoredItem.Stream, item.Stream) {
				t.Fatal("JSONL identity/accounting/metadata drift")
			}
			// Reimport uses the historical identity, never a new sanitization hash.
			if _, _, err := usageservice.New(restored).Import(ctx, bytes.NewReader(exported)); err != nil {
				t.Fatal(err)
			}
			repeated, err := New(restored).Analytics(ctx, Request{FromMS: event.TimestampMS - 1, ToMS: event.TimestampMS + 1, Include: Include{EventsPage: &EventsPage{Limit: 10}}})
			if err != nil || repeated.Events == nil || len(repeated.Events.Items) != 1 {
				t.Fatal("reimport duplicated event")
			}
			var compatible bytes.Buffer
			if err := st.WriteCompatibleUsage(ctx, &compatible, 100); err != nil {
				t.Fatal(err)
			}
			evidence = append(evidence, parityConsumerEvidence{ID: record.ID, Actual: actual, Compatible: json.RawMessage(compatible.Bytes())})
			t.Logf("case=%s semantics=%s migration_revision=%s expected=%+v actual=%+v", record.ID, record.Release, revision, record.Expected, actual)
		})
	}
	if dir := strings.TrimSpace(os.Getenv("CPAMP_PARITY_EVIDENCE_DIR")); dir != "" && !t.Failed() {
		payload, err := json.MarshalIndent(evidence, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "compatible.json"), append(payload, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
