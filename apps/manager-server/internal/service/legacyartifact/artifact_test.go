package legacyartifact

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fixtureEntry struct {
	name string
	body string
	kind byte
	size int64
}

func tarFixture(t *testing.T, entries []fixtureEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	g := gzip.NewWriter(&buf)
	w := tar.NewWriter(g)
	for _, e := range entries {
		mode := int64(0755)
		size := int64(len(e.body))
		if e.size > 0 {
			size = e.size
		}
		h := &tar.Header{Name: e.name, Mode: mode, Typeflag: e.kind, Size: size}
		if e.kind == tar.TypeSymlink {
			h.Linkname = "other"
			h.Size = 0
		}
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := w.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = w.Close()
	_ = g.Close()
	return buf.Bytes()
}

func TestArchiveParserRejectsUnsafeOrIncompleteExecutable(t *testing.T) {
	asset := "cpa-manager-plus_v1.14.4_linux_amd64.tar.gz"
	root := "cpa-manager-plus_v1.14.4_linux_amd64"
	bin := root + "/cpa-manager-plus"
	valid := fixtureEntry{name: bin, body: "synthetic executable", kind: tar.TypeReg}
	cases := map[string][]fixtureEntry{
		"missing":              {{name: root + "/README.md", body: "readme", kind: tar.TypeReg}},
		"duplicate":            {valid, valid},
		"traversal":            {valid, {name: root + "/../unexpected", body: "x", kind: tar.TypeReg}},
		"absolute":             {valid, {name: "/tmp/unexpected", body: "x", kind: tar.TypeReg}},
		"wrong-platform":       {{name: "cpa-manager-plus_v1.14.4_linux_arm64/cpa-manager-plus", body: "x", kind: tar.TypeReg}},
		"symlink":              {{name: bin, kind: tar.TypeSymlink}},
		"empty-executable":     {{name: bin, kind: tar.TypeReg}},
		"backslash":            {{name: root + "\\cpa-manager-plus", body: "x", kind: tar.TypeReg}},
		"alternate-stream":     {{name: bin + ":secret", body: "x", kind: tar.TypeReg}},
		"control-name":         {valid, {name: root + "/secret\nvalue", body: "secret", kind: tar.TypeReg}},
		"directory-executable": {{name: bin + "/", kind: tar.TypeDir}},
		"oversized-executable": {{name: bin, size: maxExecutableBytes + 1, kind: tar.TypeReg}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := inspectArchive(context.Background(), tarFixture(t, entries), asset); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	data := tarFixture(t, []fixtureEntry{valid, {name: root + "/docs/notes.md", body: "notes", kind: tar.TypeReg}})
	digest, size, err := inspectArchive(context.Background(), data, asset)
	expected := sha256.Sum256([]byte(valid.body))
	if err != nil || size != int64(len(valid.body)) || digest != hex.EncodeToString(expected[:]) {
		t.Fatalf("actual executable bytes not digested: %s %d %v", digest, size, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := inspectArchive(ctx, data, asset); err == nil {
		t.Fatal("cancelled parser succeeded")
	}
	broken := append([]byte(nil), data...)
	broken[len(broken)-1] ^= 1
	if _, _, err := inspectArchive(context.Background(), broken, asset); err == nil {
		t.Fatal("corrupt gzip trailer succeeded")
	}
}

func zipFixture(t *testing.T, entries []fixtureEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		h.SetMode(0600)
		if e.kind == tar.TypeSymlink {
			h.SetMode(os.ModeSymlink | 0777)
		}
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestWindowsArchiveParserUsesExactExecutableAndChecksCRC(t *testing.T) {
	asset := "cpa-manager-plus_v1.14.4_windows_amd64.zip"
	root := "cpa-manager-plus_v1.14.4_windows_amd64"
	valid := fixtureEntry{name: root + "/cpa-manager-plus.exe", body: "synthetic exe"}
	data := zipFixture(t, []fixtureEntry{valid})
	digest, size, err := inspectArchive(context.Background(), data, asset)
	expected := sha256.Sum256([]byte(valid.body))
	if err != nil || size != int64(len(valid.body)) || digest != hex.EncodeToString(expected[:]) {
		t.Fatal("zip executable digest", digest, size, err)
	}
	for name, entries := range map[string][]fixtureEntry{
		"duplicate": {valid, valid}, "symlink": {{name: valid.name, body: "other", kind: tar.TypeSymlink}},
		"wrong-name": {{name: root + "/cpa-manager-plus", body: "x"}},
		"traversal":  {valid, {name: root + "/../escape", body: "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := inspectArchive(context.Background(), zipFixture(t, entries), asset); err == nil {
				t.Fatal("unsafe zip accepted")
			}
		})
	}
	// Tamper the central-directory CRC while preserving the compressed payload.
	broken := append([]byte(nil), data...)
	index := bytes.Index(broken, []byte{'P', 'K', 1, 2})
	if index < 0 {
		t.Fatal("fixture central directory missing")
	}
	broken[index+16] ^= 1
	if _, _, err := inspectArchive(context.Background(), broken, asset); err == nil {
		t.Fatal("zip CRC not checked")
	}
}

func TestVerifyRejectsUserArtifactAssertionsAndPreservesSource(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "archive")
	payload := tarFixture(t, []fixtureEntry{{name: "cpa-manager-plus_v1.14.4_linux_amd64/cpa-manager-plus", body: Release + " " + SourceCommit, kind: tar.TypeReg}})
	if err := os.WriteFile(p, payload, 0600); err != nil {
		t.Fatal(err)
	}
	for _, asset := range []string{"cpa-manager-plus_v1.14.4_linux_amd64.tar.gz", "cpa-manager-plus_v1.14.5_linux_amd64.tar.gz", "../cpa-manager-plus_v1.14.4_linux_amd64.tar.gz"} {
		report, err := Verify(context.Background(), p, asset)
		if !errors.Is(err, ErrUnverified) || !reflect.DeepEqual(report, Report{}) {
			t.Fatalf("user artifact assertion verified: %+v %v", report, err)
		}
	}
	after, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(after, payload) {
		t.Fatal("source changed")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("verification created source files")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if report, err := Verify(ctx, p, "cpa-manager-plus_v1.14.4_linux_amd64.tar.gz"); err == nil || report.Status != "" {
		t.Fatal("cancelled verification succeeded")
	}
	if report, err := Verify(context.Background(), dir, "cpa-manager-plus_v1.14.4_linux_amd64.tar.gz"); err == nil || report.Status != "" {
		t.Fatal("directory accepted")
	}
	link := filepath.Join(dir, "link")
	if os.Symlink(p, link) == nil {
		if _, err := Verify(context.Background(), link, "cpa-manager-plus_v1.14.4_linux_amd64.tar.gz"); err == nil {
			t.Fatal("symlink accepted")
		}
	}
	big := filepath.Join(dir, "big")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxArchiveBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := Verify(context.Background(), big, "cpa-manager-plus_v1.14.4_linux_amd64.tar.gz"); err == nil {
		t.Fatal("oversized archive accepted")
	}
}

// Optional local qualification reads actual immutable published bytes. The
// small synthetic parser fixtures above are never published-artifact proof.
func TestPublishedNativeArchiveQualification(t *testing.T) {
	p, asset := os.Getenv("CPAMP_LEGACY_ARTIFACT_ARCHIVE"), os.Getenv("CPAMP_LEGACY_ARTIFACT_ASSET")
	if p == "" || asset == "" {
		t.Skip("requires separately downloaded immutable v1.14.4 native archive")
	}
	report, err := Verify(context.Background(), p, asset)
	if err != nil || report.Status != "verified-artifact-only" || report.Release != Release || report.SourceCommit != SourceCommit || report.ExecutableBytes <= 0 || report.WriterEvidence || report.UpgradeAuthorized {
		t.Fatalf("published artifact qualification: %+v %v", report, err)
	}
}
