// Package src11 is opt-in qualification tooling, never production admission.
package src11

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/monitoring"
)

const sourceCommit = "166262365d506157b99968105c890f480cc7c07c"
const planCommit = "26b5d6bde1c30e8187135c87a8b84b6c808f23f0"
const asset = "cpa-manager-plus_v1.14.4_linux_amd64.tar.gz"
const releaseInfoDigest = "b8f7b97cd242c11d95b905e2b402275fc5d664025bc829773a260d1da17d18b5"

// Embedded only after the runner verifies committed harness bytes.
var generatorCommit string

type profile struct {
	Profile     string                      `json:"profile"`
	Events      []json.RawMessage           `json:"events"`
	Prices      map[string]model.ModelPrice `json:"prices"`
	Quota       json.RawMessage             `json:"quota"`
	FromMS      int64                       `json:"fromMS"`
	ToMS        int64                       `json:"toMS"`
	ExpectedUSD map[string]float64          `json:"expectedUSD"`
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func digestBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func fileDigest(t *testing.T, path string) string {
	t.Helper()
	f, e := os.Open(path)
	must(t, e)
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	must(t, e)
	return hex.EncodeToString(h.Sum(nil))
}
func read(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(path)
	must(t, e)
	return b
}
func save(t *testing.T, root, name string, value any) string {
	t.Helper()
	b, e := json.MarshalIndent(value, "", "  ")
	must(t, e)
	b = append(b, '\n')
	p := filepath.Join(root, name)
	must(t, os.MkdirAll(filepath.Dir(p), 0700))
	must(t, os.WriteFile(p, b, 0600))
	return digestBytes(b)
}
func loadProfile(t *testing.T, path string) (profile, []byte) {
	t.Helper()
	b := read(t, path)
	var p profile
	must(t, json.Unmarshal(b, &p))
	if (p.Profile != "src-11-full-raw-revision2-v1" && p.Profile != "ret-01-full-raw-revision2-v1" && p.Profile != ret02Profile && p.Profile != mig01Profile && p.Profile != mig02Profile) || len(p.Events) != 4 || len(p.ExpectedUSD) != 4 {
		t.Fatal("unknown/incomplete profile")
	}
	return p, b
}
func dbURI(path string, readonly bool) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := url.Values{}
	if readonly {
		q.Set("mode", "ro")
	}
	u.RawQuery = q.Encode()
	return u.String()
}
func openDB(t *testing.T, path string, readonly bool) *sql.DB {
	t.Helper()
	db, e := sql.Open("sqlite", dbURI(path, readonly))
	must(t, e)
	db.SetMaxOpenConns(1)
	must(t, db.Ping())
	t.Cleanup(func() { db.Close() })
	return db
}
func query(t *testing.T, db *sql.DB, q string, args ...any) []map[string]any {
	t.Helper()
	rows, e := db.Query(q, args...)
	must(t, e)
	defer rows.Close()
	cols, e := rows.Columns()
	must(t, e)
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range vals {
			ptr[i] = &vals[i]
		}
		must(t, rows.Scan(ptr...))
		row := map[string]any{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			row[c] = vals[i]
		}
		out = append(out, row)
	}
	must(t, rows.Err())
	return out
}
func scalar(t *testing.T, db *sql.DB, q string) int64 {
	t.Helper()
	var n int64
	must(t, db.QueryRow(q).Scan(&n))
	return n
}
func snapshot(t *testing.T, db *sql.DB) map[string][]map[string]any {
	t.Helper()
	out := map[string][]map[string]any{}
	for _, table := range []string{"usage_events", "model_prices", "model_price_context_tiers", "model_price_service_tiers", "account_quota_observations", "account_quota_windows", "account_quota_window_activations", "account_quota_cycles", "account_quota_snapshots", "quota_cooldowns"} {
		out[table] = query(t, db, "select * from "+table+" order by rowid")
	}
	return out
}
func assertPreserved(t *testing.T, before, after map[string][]map[string]any) {
	t.Helper()
	derived := map[string]bool{"cache_input_mode": true, "normalized_uncached_input_tokens": true, "normalized_total_input_tokens": true, "normalized_cache_read_tokens": true, "normalized_cache_creation_tokens": true}
	for table, rows := range before {
		if len(rows) != len(after[table]) {
			t.Fatalf("%s row count changed", table)
		}
		for i, row := range rows {
			for col, want := range row {
				if table == "usage_events" && derived[col] {
					continue
				}
				if !reflect.DeepEqual(want, after[table][i][col]) {
					t.Fatalf("authoritative %s row %d column %s changed: %v -> %v", table, i, col, want, after[table][i][col])
				}
			}
		}
	}
}
func sqliteChecks(t *testing.T, db *sql.DB, checkpoint bool) map[string]any {
	t.Helper()
	rows := query(t, db, "pragma integrity_check")
	if len(rows) != 1 || rows[0]["integrity_check"] != "ok" {
		t.Fatalf("integrity: %v", rows)
	}
	fk := query(t, db, "pragma foreign_key_check")
	if len(fk) != 0 {
		t.Fatalf("foreign keys: %v", fk)
	}
	out := map[string]any{"integrityCheck": rows, "foreignKeyCheck": fk, "journalMode": query(t, db, "pragma journal_mode")}
	if checkpoint {
		c := query(t, db, "pragma wal_checkpoint(truncate)")
		if len(c) != 1 || c[0]["busy"] != int64(0) || c[0]["log"] != int64(0) || c[0]["checkpointed"] != int64(0) {
			t.Fatalf("checkpoint: %v", c)
		}
		out["checkpoint"] = c
	}
	return out
}
func analyticsRequest(p profile, modelName string) monitoring.Request {
	r := monitoring.Request{FromMS: p.FromMS, ToMS: p.ToMS, NowMS: p.ToMS, TimeZone: "UTC", Include: monitoring.Include{Summary: true}}
	if modelName != "" {
		r.Filters.Models = []string{modelName}
	}
	return r
}
func httpCall(t *testing.T, method, base, path string, body any) []byte {
	t.Helper()
	var input io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		must(t, e)
		input = strings.NewReader(string(b))
	}
	req, e := http.NewRequest(method, base+path, input)
	must(t, e)
	req.Header.Set("Authorization", "Bearer qualification-only-admin-key")
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, e := client.Do(req)
	must(t, e)
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	must(t, e)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("%s %s status %d: %s", method, path, resp.StatusCode, b)
	}
	return b
}
func wait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("qualification observation timed out")
}
func envRequired(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("%s is required", key)
	}
	return v
}
func codeIdentity(t *testing.T) string {
	t.Helper()
	if len(generatorCommit) != 40 {
		t.Fatal("runner must embed verified GeneratorCodeCommit")
	}
	_, e := hex.DecodeString(generatorCommit)
	must(t, e)
	return generatorCommit
}
func inventory(t *testing.T, root string) maintenancebundle.Manifest {
	t.Helper()
	m, e := maintenancebundle.Inspect(context.Background(), maintenancebundle.BundleLayout(root))
	must(t, e)
	return m
}
func sameInventory(t *testing.T, a, b maintenancebundle.Manifest) {
	t.Helper()
	if a.SourceRevision != b.SourceRevision || !reflect.DeepEqual(a.Entries, b.Entries) {
		t.Fatal("bundle content/inventory changed")
	}
}
func platformIdentity() map[string]any {
	return map[string]any{"goVersion": runtime.Version(), "goOS": runtime.GOOS, "goArch": runtime.GOARCH, "qualificationOnly": true}
}
func sortedModels(p profile) []string {
	keys := []string{}
	for k := range p.ExpectedUSD {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func assertClose(t *testing.T, name string, got, want float64) {
	t.Helper()
	d := got - want
	if d < 0 {
		d = -d
	}
	if d > 1e-12 {
		t.Fatalf("%s USD %.12f != %.12f", name, got, want)
	}
}
func checkFullRaw(t *testing.T, db *sql.DB) {
	t.Helper()
	if scalar(t, db, "select count(*) from usage_events") != 4 || scalar(t, db, "select count(*) from usage_archive_runs") != 0 || scalar(t, db, "select count(*) from usage_archive_segments") != 0 {
		t.Fatal("SRC-11 is not full raw with zero source archives")
	}
}
