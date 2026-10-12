package legacypreflight

import (
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

//go:embed testdata/legacy-profiles/schema-fixtures.json
var legacyProfileFixtureJSON []byte

type legacyProfileFixture struct {
	Ref          string `json:"ref"`
	Commit       string `json:"commit"`
	SourceBlob   string `json:"sourceBlob"`
	SourceSha256 string `json:"sourceSha256"`
	SourcePath   string `json:"sourcePath"`
	Family       string `json:"family"`
	DDL          []struct {
		Table  string `json:"table"`
		SQL    string `json:"sql"`
		SHA256 string `json:"sha256"`
	} `json:"ddl"`
}

func loadLegacyProfileFixtures(t *testing.T) []legacyProfileFixture {
	t.Helper()
	var data struct {
		Format   int                    `json:"format"`
		Fixtures []legacyProfileFixture `json:"fixtures"`
	}
	if err := json.Unmarshal(legacyProfileFixtureJSON, &data); err != nil {
		t.Fatal(err)
	}
	if data.Format != 1 || len(data.Fixtures) != 7 {
		t.Fatal("wrong fixture format/count")
	}
	return data.Fixtures
}
func buildLegacyProfileFixture(t *testing.T, fixture legacyProfileFixture) sqlite.InspectionPaths {
	t.Helper()
	paths := fixturePaths(t)
	db, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range fixture.DDL {
		if _, err := db.Exec(ddl.SQL); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return paths
}
func execLegacyFixture(t *testing.T, path, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestLegacyProfileFixtureProvenance(t *testing.T) {
	expected := map[string]string{
		"v1.12.12": "e19d8267a52ca146c43bdb86d185bb7baed7ff38",
		"v1.13.0":  "ff361648f0b6d54bb678ae555cd86169142635d0",
		"v1.13.1":  "e188cd8ac4a9ef7a9875d37480a301f29997905d",
		"v1.13.2":  "1fa5e8a79ce067350d9a5ba6dd5a7b1843cc77f4",
		"v1.14.0":  "eea422fd6a19ddb90f117b79b80d1f785a3b6a83",
		"v1.14.3":  "fb3e8f501f47a29b0fa66a5bf31f0f36739750d3",
		"v1.14.4":  "166262365d506157b99968105c890f480cc7c07c",
	}
	seen := map[string]bool{}
	for _, fixture := range loadLegacyProfileFixtures(t) {
		if fixture.Commit != expected[fixture.Ref] || seen[fixture.Ref] {
			t.Fatal("unreviewed/duplicate source", fixture.Ref)
		}
		seen[fixture.Ref] = true
		sourceDigest, err := hex.DecodeString(fixture.SourceSha256)
		blob, blobErr := hex.DecodeString(fixture.SourceBlob)
		if err != nil || len(sourceDigest) != 32 || blobErr != nil || len(blob) != 20 || fixture.SourcePath != "apps/manager-server/internal/repository/sqlite/migrate.go" {
			t.Fatal("invalid source provenance")
		}
		seenTables := map[string]bool{}
		for _, ddl := range fixture.DDL {
			sum := sha256.Sum256([]byte(ddl.SQL))
			if hex.EncodeToString(sum[:]) != ddl.SHA256 || seenTables[ddl.Table] {
				t.Fatal("invalid DDL hash/duplicate")
			}
			seenTables[ddl.Table] = true
		}
	}
}
