package legacyartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/maintenancebundle"
)

var ErrEmptyManifest = errors.New("cannot compute digest for empty bundle manifest")

// ComputeManifestDigest calculates a deterministic, canonical SHA256 digest
// of all entries in a maintenancebundle.Manifest. Any addition, deletion,
// or modification of a file, WAL, SHM, journal, key or archive entry changes
// this digest.
func ComputeManifestDigest(m maintenancebundle.Manifest) (string, error) {
	if len(m.Entries) == 0 {
		return "", ErrEmptyManifest
	}
	keys := make([]string, 0, len(m.Entries))
	for k := range m.Entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("v%d:%s\n", m.Version, m.Fence))
	for _, k := range keys {
		e := m.Entries[k]
		b.WriteString(fmt.Sprintf("%s|p:%t|d:%t|s:%d|h:%s\n", k, e.Present, e.Directory, e.Size, e.SHA256))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:]), nil
}
