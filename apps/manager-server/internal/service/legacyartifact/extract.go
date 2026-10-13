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
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrExtractionFailed = errors.New("failed to extract verified executable from archive")
)

// ExtractExecutable verifies the given archive against its native pin, extracts
// only the authentic bridge executable to targetExePath, verifies the extracted
// file bytes on disk, and marks it executable (0700).
func ExtractExecutable(ctx context.Context, archivePath, asset, targetExePath string) (string, int64, error) {
	pin, ok := nativePins[asset]
	if !ok || ctx.Err() != nil {
		return "", 0, ErrUnverified
	}
	info, err := os.Lstat(archivePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxArchiveBytes {
		return "", 0, ErrUnverified
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return "", 0, ErrUnverified
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", 0, ErrUnverified
	}
	data, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, r: f}, maxArchiveBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxArchiveBytes {
		return "", 0, ErrUnverified
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != pin {
		return "", 0, ErrUnverified
	}

	exeDigest, exeSize, err := extractExecutableFromData(ctx, data, asset, targetExePath)
	if err != nil {
		_ = os.Remove(targetExePath)
		return "", 0, err
	}
	return exeDigest, exeSize, nil
}

func extractExecutableFromData(ctx context.Context, data []byte, asset, targetExePath string) (string, int64, error) {
	isZip := strings.HasSuffix(asset, ".zip")
	suffix := ".tar.gz"
	bin := "cpa-manager-plus"
	if isZip {
		suffix = ".zip"
		bin += ".exe"
	}
	root := strings.TrimSuffix(asset, suffix)
	executableName := root + "/" + bin

	if err := os.MkdirAll(filepath.Dir(targetExePath), 0o700); err != nil {
		return "", 0, ErrExtractionFailed
	}

	out, err := os.OpenFile(targetExePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return "", 0, ErrExtractionFailed
	}
	defer out.Close()

	hash := sha256.New()
	multi := io.MultiWriter(out, hash)
	var extractedSize int64
	var found bool

	if isZip {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil || len(z.File) > maxEntries {
			return "", 0, ErrUnverified
		}
		for _, f := range z.File {
			if f.Name == executableName {
				if f.FileInfo().IsDir() || !f.Mode().IsRegular() || f.UncompressedSize64 > maxExecutableBytes || f.UncompressedSize64 == 0 {
					return "", 0, ErrUnverified
				}
				r, err := f.Open()
				if err != nil {
					return "", 0, ErrUnverified
				}
				n, err := io.Copy(multi, &contextReader{ctx: ctx, r: io.LimitReader(r, int64(f.UncompressedSize64)+1)})
				_ = r.Close()
				if err != nil || n != int64(f.UncompressedSize64) {
					return "", 0, ErrUnverified
				}
				extractedSize = n
				found = true
				break
			}
		}
	} else {
		g, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return "", 0, ErrUnverified
		}
		defer g.Close()
		expanded := &io.LimitedReader{R: &contextReader{ctx: ctx, r: g}, N: maxExpandedBytes + 1}
		t := tar.NewReader(expanded)
		for {
			h, err := t.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", 0, ErrUnverified
			}
			if h.Name == executableName {
				if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA || h.Size <= 0 || h.Size > maxExecutableBytes {
					return "", 0, ErrUnverified
				}
				n, err := io.Copy(multi, &contextReader{ctx: ctx, r: io.LimitReader(t, h.Size+1)})
				if err != nil || n != h.Size {
					return "", 0, ErrUnverified
				}
				extractedSize = n
				found = true
				break
			}
		}
	}

	if !found || extractedSize <= 0 || ctx.Err() != nil {
		return "", 0, ErrUnverified
	}

	if err := out.Sync(); err != nil {
		return "", 0, ErrExtractionFailed
	}
	if err := out.Close(); err != nil {
		return "", 0, ErrExtractionFailed
	}

	// Verify on-disk file independently
	diskData, err := os.ReadFile(targetExePath)
	if err != nil || int64(len(diskData)) != extractedSize {
		return "", 0, ErrExtractionFailed
	}
	diskHash := sha256.Sum256(diskData)
	computedDigest := hex.EncodeToString(hash.Sum(nil))
	if computedDigest != hex.EncodeToString(diskHash[:]) {
		return "", 0, ErrExtractionFailed
	}
	return computedDigest, extractedSize, nil
}
