package httpapi

import (
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "io/fs"
    "os"
    "strings"
    "testing"
)

// Checks the complete real compiled embed, not just links referenced by HTML.
// The expected manifest is created from the same built Web dist before go build.
func TestPackagedFullWebManifest(t *testing.T) {
    if os.Getenv("CPAMP_FULLWEB_PACKAGED_TEST") != "1" {
        t.Skip("requires Docker staged Full Web output")
    }
    raw, err := os.ReadFile("release-fullweb-manifest.json")
    if err != nil { t.Fatal(err) }
    var expected struct {
        SchemaVersion int `json:"schemaVersion"`
        Files []struct { Path string `json:"path"`; SHA256 string `json:"sha256"` } `json:"files"`
    }
    if err := json.Unmarshal(raw, &expected); err != nil { t.Fatal(err) }
    if expected.SchemaVersion != 1 || len(expected.Files) < 2 { t.Fatal("invalid dist manifest") }
    seen := make(map[string]bool)
    for _, item := range expected.Files {
        if item.Path == "" || strings.Contains(item.Path, "..") || seen[item.Path] {
            t.Fatalf("invalid or duplicate asset %q", item.Path)
        }
        seen[item.Path] = true
        content, err := fs.ReadFile(embeddedPanel, "web/fullweb/"+item.Path)
        if err != nil { t.Fatal(err) }
        sum := sha256.Sum256(content)
        if "sha256:"+hex.EncodeToString(sum[:]) != item.SHA256 {
            t.Fatalf("embedded Full Web differs from dist: %s", item.Path)
        }
    }
    if !seen["index.html"] { t.Fatal("missing Full Web index") }
    count := 0
    if err := fs.WalkDir(embeddedPanel, "web/fullweb", func(name string, entry fs.DirEntry, err error) error {
        if err != nil { return err }
        if entry.IsDir() { return nil }
        count++
        if !seen[strings.TrimPrefix(name, "web/fullweb/")] {
            t.Errorf("extra embedded file %s", name)
        }
        return nil
    }); err != nil { t.Fatal(err) }
    if count != len(expected.Files) { t.Fatalf("file set differs: embedded %d, expected %d", count, len(expected.Files)) }
}
