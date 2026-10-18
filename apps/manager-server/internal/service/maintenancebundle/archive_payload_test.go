package maintenancebundle

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	usageService "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/usage"
)

// These are the original fixed source bytes, never regenerated runtime evidence.
func TestMatchingFixedArchivePayload(t *testing.T) {
	for _, fixture := range []struct{ name, root string }{
		{"BAK-02", "../../../../../tests/fixtures/phase4a-evidence/bak-02/bundle"},
		{"RET-07", os.Getenv("CPAMP_TEST_RET07_BUNDLE")},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if fixture.root == "" {
				t.Skip("set CPAMP_TEST_RET07_BUNDLE to the materialized pinned RET-07 bundle")
			}
			root, err := filepath.Abs(fixture.root)
			if err != nil {
				t.Fatal(err)
			}
			l := BundleLayout(root)
			before, err := Inspect(context.Background(), l)
			if err != nil {
				t.Fatal(err)
			}
			// Git preserves bytes, not private 0600 modes. Materialize through
			// the existing safe copier without chmod/opening the fixed SQLite.
			copyRoot, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := materialize(context.Background(), l, copyRoot, before, copyFile); err != nil {
				t.Fatal(err)
			}
			copyLayout := BundleLayout(copyRoot)
			copyBefore, err := Inspect(context.Background(), copyLayout)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateMatchingEvidence(context.Background(), copyLayout); err != nil {
				t.Fatalf("fixed published archive rejected: %v", err)
			}
			if err := Revalidate(context.Background(), copyLayout, copyBefore); err != nil {
				t.Fatalf("private fixed copy changed: %v", err)
			}
			if err := Revalidate(context.Background(), l, before); err != nil {
				t.Fatalf("fixed source bytes changed: %v", err)
			}
		})
	}
}

func TestMatchingPublishedArchivePayloadRejectsConsistentOuterDigests(t *testing.T) {
	for _, change := range []string{
		"manifest-json", "manifest-run", "manifest-suffix", "cancelled-empty-manifest-json", "gzip", "gzip-crc", "jsonl",
		"envelope", "event-hash", "event-order", "segment-count", "run-count", "archived-count", "last-archived-id", "run-bytes", "event-digest",
	} {
		t.Run(change, func(t *testing.T) {
			l, root, lock := archiveLifecycleFixture(t, "archived", "")
			bundle, _ := mustCreate(t, l, root, lock)
			layout := BundleLayout(bundle)
			manifestPath := filepath.Join(layout.ArchivesPath, "00000000000000000000000000000001", "manifest.json")
			payload, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var manifest usageService.ArchiveManifest
			if err := json.Unmarshal(payload, &manifest); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", layout.DatabasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := db.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch change {
			case "cancelled-empty-manifest-json":
				exec("DELETE FROM usage_archive_event_refs")
				exec("DELETE FROM usage_archive_segments")
				exec("UPDATE usage_archive_runs SET status = 'cancelled'")
			case "manifest-run":
				manifest.RunID = strings.Repeat("f", 32)
			case "run-count":
				manifest.EventCount++
				exec("UPDATE usage_archive_runs SET event_count = ?", manifest.EventCount)
			case "archived-count":
				exec("UPDATE usage_archive_runs SET archived_event_count = archived_event_count + 1")
			case "last-archived-id":
				exec("UPDATE usage_archive_runs SET last_archived_event_id = last_archived_event_id + 1")
			case "run-bytes":
				manifest.UncompressedBytes++
				exec("UPDATE usage_archive_runs SET archived_uncompressed_bytes = ?", manifest.UncompressedBytes)
			case "event-digest":
				manifest.EventHashDigest = strings.Repeat("0", 64)
				exec("UPDATE usage_archive_runs SET archive_digest = ?", manifest.EventHashDigest)
			case "segment-count":
				manifest.Segments[0].EventCount++
				exec("UPDATE usage_archive_segments SET event_count = ?", manifest.Segments[0].EventCount)
			case "gzip", "gzip-crc", "jsonl", "envelope", "event-hash", "event-order":
				segment := &manifest.Segments[0]
				segmentPath := filepath.Join(layout.ArchivesPath, filepath.FromSlash(segment.FileName))
				compressed, err := os.ReadFile(segmentPath)
				if err != nil {
					t.Fatal(err)
				}
				if change == "gzip" {
					compressed[0] ^= 1
				} else if change == "gzip-crc" {
					compressed[len(compressed)-8] ^= 1
				} else {
					zipper, err := gzip.NewReader(bytes.NewReader(compressed))
					if err != nil {
						t.Fatal(err)
					}
					record, err := io.ReadAll(zipper)
					if err != nil {
						t.Fatal(err)
					}
					if err := zipper.Close(); err != nil {
						t.Fatal(err)
					}
					switch change {
					case "jsonl":
						record = []byte("{invalid-json}\n")
					case "event-order":
						record = append(record, record...)
					default:
						var value map[string]any
						if err := json.Unmarshal(record, &value); err != nil {
							t.Fatal(err)
						}
						if change == "envelope" {
							value["_cpamp_archive_event_id"] = 0
						} else {
							value["event_hash"] = "noncanonical"
						}
						record, err = json.Marshal(value)
						if err != nil {
							t.Fatal(err)
						}
						record = append(record, '\n')
					}
					var output bytes.Buffer
					writer := gzip.NewWriter(&output)
					if _, err := writer.Write(record); err != nil {
						t.Fatal(err)
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					compressed = output.Bytes()
				}
				put(t, segmentPath, compressed)
				digest := sha256.Sum256(compressed)
				segment.ContentSHA256 = hex.EncodeToString(digest[:])
				segment.CompressedBytes = int64(len(compressed))
				manifest.CompressedBytes = segment.CompressedBytes
				exec("UPDATE usage_archive_segments SET content_sha256 = ?, compressed_bytes = ?", segment.ContentSHA256, segment.CompressedBytes)
				exec("UPDATE usage_archive_runs SET archived_compressed_bytes = ?", manifest.CompressedBytes)
			}
			payload, err = json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if change == "manifest-json" || change == "cancelled-empty-manifest-json" {
				payload = []byte("{invalid-json}")
			} else if change == "manifest-suffix" {
				payload = append(payload, []byte("\n{}")...)
			}
			put(t, manifestPath, payload)
			digest := sha256.Sum256(payload)
			exec("UPDATE usage_archive_runs SET manifest_sha256 = ?", hex.EncodeToString(digest[:]))
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			rewriteManifest(t, bundle)
			before, err := Verify(context.Background(), bundle)
			if err != nil {
				t.Fatalf("outer bundle checks must pass: %v", err)
			}
			// Explicitly prove the old archive digest/lifecycle gates also pass.
			control, err := sql.Open("sqlite", sqliteURI(layout.DatabasePath)+"&immutable=1")
			if err != nil {
				t.Fatal(err)
			}
			segments := map[string]archiveSegmentEvidence{}
			if err := validateArchiveSegments(context.Background(), control, layout.ArchivesPath, segments); err != nil {
				t.Fatalf("outer segment observations must match: %v", err)
			}
			if err := validateArchiveRunManifests(context.Background(), control, layout.ArchivesPath, segments, map[string]string{}); err != nil {
				t.Fatalf("outer manifest observations must match: %v", err)
			}
			if err := control.Close(); err != nil {
				t.Fatal(err)
			}
			if err := ValidateMatchingEvidence(context.Background(), layout); err == nil || !strings.Contains(err.Error(), "archive published payload") {
				t.Fatalf("decoded corruption must fail in the shared reader: %v", err)
			}
			if err := Revalidate(context.Background(), layout, before); err != nil {
				t.Fatalf("rejected backup changed: %v", err)
			}
		})
	}
}

func TestMatchingArchivePayloadReadOnlyAndCancellation(t *testing.T) {
	l, root, lock := archiveLifecycleFixture(t, "completed", "")
	bundle, _ := mustCreate(t, l, root, lock)
	for _, layout := range []Layout{l, BundleLayout(bundle)} {
		before, err := Inspect(context.Background(), layout)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateMatchingEvidence(context.Background(), layout); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := ValidateMatchingEvidence(ctx, layout); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled inspection: %v", err)
		}
		if err := Revalidate(context.Background(), layout, before); err != nil {
			t.Fatalf("source/backup changed: %v", err)
		}
	}
}
