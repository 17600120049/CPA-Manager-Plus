package src11

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

const (
	bak02CaseID     = "BAK-02"
	bak02PlanCommit = "21035045c425e453bd8f3007619fc0734861012e"
	bak02Profile    = "bak-02-matching-bundle-v1"
)

// These records bind every cross-asset identity used by the matching-bundle
// case. The archive inventory is produced by the real v1.14.4 archive writer;
// the identity records contain digests and metadata only, never key material.
var bak02GenerationFiles = []string{
	"controlled-input.json",
	"release-info.json",
	"producer-build.json",
	"producer.log",
	"config-write.json",
	"price-write.json",
	"quota-write.json",
	"v1-analytics.json",
	"v1-export.jsonl",
	"source-snapshot.json",
}

func init() {
	bak02GenerationFiles = append(bak02GenerationFiles, ret02GenerationFiles...)
	bak02GenerationFiles = append(bak02GenerationFiles,
		"database-identity.json",
		"key-identity.json",
		"config-identity.json",
		"matching-snapshot.json",
		"authoritative-digest.json",
		"backup-execution.json",
	)
}

func bak02ManifestNames(m maintenancebundle.Manifest) []string {
	names := make([]string, 0, len(m.Entries))
	for name := range m.Entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func readJSONValue(t *testing.T, path string) any {
	t.Helper()
	var value any
	must(t, json.Unmarshal(read(t, path), &value))
	return value
}
