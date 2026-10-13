// Package legacyartifact verifies the exact published bridge writer artifact.
// It neither executes that writer nor establishes the identity of any database.
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
	"path"
	"strings"
)

const (
	Release            = "v1.14.4"
	SourceCommit       = "166262365d506157b99968105c890f480cc7c07c"
	maxArchiveBytes    = 16 << 20
	maxExpandedBytes   = 256 << 20
	maxExecutableBytes = 64 << 20
	maxEntries         = 4096
)

// These digests were independently checked against immutable GitHub release
// 404941931 and its release-info.json (SHA256 b8f7b97cd242c11d95b905e2b402275fc5d664025bc829773a260d1da17d18b5).
// A user-provided checksum, release string, or executable --version cannot
// replace this deployment-owned pin. Pins include the exact published archive
// bytes; the executable digest is derived from those authenticated bytes.
type nativePin struct {
	ArchiveSHA256    string
	ExecutableSHA256 string
}

var nativePins = map[string]nativePin{
	"cpa-manager-plus_v1.14.4_darwin_amd64.tar.gz": {ArchiveSHA256: "ea55a5e85ceeaf63f430d965727bff5de7c85c4305f6e4d3d1eb9c87af6410d1", ExecutableSHA256: "7c320f6109768a76c8ca93e2db62238fd99442cb1687f76c433b37b8a98ec4ce"},
	"cpa-manager-plus_v1.14.4_darwin_arm64.tar.gz": {ArchiveSHA256: "1ae54030965ea850195d6e0c62a1f2a2e319b220524fbccb60de36cb046f5a14", ExecutableSHA256: "a91ae73b46e0db6b953031ba94c6c4d404e26d438aca6854a8a7855e84011ecc"},
	"cpa-manager-plus_v1.14.4_linux_amd64.tar.gz":  {ArchiveSHA256: "eab3a90594cb70e0362b0a37f9737f7b249402c0971ff4c7134a53079de1e033", ExecutableSHA256: "0588b7f28010825cde26511dfecafbb4a70f6c26d35f4cc4998b2f90434b3a98"},
	"cpa-manager-plus_v1.14.4_linux_arm64.tar.gz":  {ArchiveSHA256: "9290e986cff0867b8348c1f1ef0a685354f86cb51e9724aef60060ecca6446ee", ExecutableSHA256: "8b487e20642916aefcd70abc7da2c8cc25fd82c2f157382398694e45f36c1922"},
	"cpa-manager-plus_v1.14.4_windows_amd64.zip":   {ArchiveSHA256: "42b5ddafdbee6eee9d67cecfaf575be02e7ae74cb145443720c17b8120904bb1", ExecutableSHA256: "6fdff929d387ef405ce746a58188032ee8e4833d73f5cc30e76b106ed7b5d96a"},
	"cpa-manager-plus_v1.14.4_windows_arm64.zip":   {ArchiveSHA256: "a9c7b75d3b6d994a012c001a627bb2d11773dc7c73ab362441cb9bbc0cbef4a4", ExecutableSHA256: "46643e19447b50b5b7409bbfa0d638e5d41489dacfe9b470cd68803bbff0b9d7"},
}

var ErrUnverified = errors.New("legacy writer artifact could not be verified")

// Report is diagnostic information, not a trusted execution handle or receipt.
// Future execution must reverify its own immutable bytes and bind the writer to
// its controlled input/output bundle. Reusing this JSON never grants authority.
type Report struct {
	Status            string `json:"status"`
	Release           string `json:"release"`
	SourceCommit      string `json:"sourceCommit"`
	Asset             string `json:"asset"`
	ArchiveSHA256     string `json:"archiveSHA256"`
	ExecutableSHA256  string `json:"executableSHA256"`
	ExecutableBytes   int64  `json:"executableBytes"`
	WriterEvidence    bool   `json:"writerEvidence"`
	UpgradeAuthorized bool   `json:"upgradeAuthorized"`
}

// Verify reads a bounded local archive once. It performs no extraction, network
// calls, config discovery, database access, or process execution. All failures
// return an empty report and a fixed error, without echoing paths or payloads.
func Verify(ctx context.Context, archivePath, asset string) (Report, error) {
	pin, ok := nativePins[asset]
	if !ok || ctx.Err() != nil {
		return Report{}, ErrUnverified
	}
	info, err := os.Lstat(archivePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxArchiveBytes {
		return Report{}, ErrUnverified
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return Report{}, ErrUnverified
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return Report{}, ErrUnverified
	}
	data, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, r: f}, maxArchiveBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxArchiveBytes {
		return Report{}, ErrUnverified
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != pin.ArchiveSHA256 {
		return Report{}, ErrUnverified
	}
	// Parse exactly the in-memory bytes that passed the deployment-owned pin,
	// never reopen a mutable path after verification.
	executableDigest, executableSize, err := inspectArchive(ctx, data, asset)
	if err != nil || ctx.Err() != nil || executableDigest != pin.ExecutableSHA256 {
		return Report{}, ErrUnverified
	}
	return Report{Status: "verified-artifact-only", Release: Release, SourceCommit: SourceCommit, Asset: asset,
		ArchiveSHA256: pin.ArchiveSHA256, ExecutableSHA256: executableDigest, ExecutableBytes: executableSize}, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

func inspectArchive(ctx context.Context, data []byte, asset string) (string, int64, error) {
	isZip := strings.HasSuffix(asset, ".zip")
	suffix := ".tar.gz"
	bin := "cpa-manager-plus"
	if isZip {
		suffix = ".zip"
		bin += ".exe"
	}
	root := strings.TrimSuffix(asset, suffix)
	executable := root + "/" + bin
	seen := map[string]bool{}
	var total, executableSize int64
	var executableDigest string
	checkName := func(name string, dir bool) error {
		if dir {
			name = strings.TrimSuffix(name, "/")
		}
		if name == "" || strings.Contains(name, "\\") || strings.ContainsAny(name, "\x00:\r\n") || path.Clean(name) != name ||
			(name != root && !strings.HasPrefix(name, root+"/")) || seen[name] || len(seen) >= maxEntries {
			return ErrUnverified
		}
		if name == root && !dir {
			return ErrUnverified
		}
		seen[name] = true
		return nil
	}
	consume := func(name string, size int64, r io.Reader) error {
		if size < 0 || size > maxExpandedBytes-total {
			return ErrUnverified
		}
		total += size
		var dst io.Writer = io.Discard
		hash := sha256.New()
		if name == executable {
			if size <= 0 || size > maxExecutableBytes {
				return ErrUnverified
			}
			dst = hash
		}
		n, err := io.Copy(dst, &contextReader{ctx: ctx, r: io.LimitReader(r, size+1)})
		if err != nil || n != size {
			return ErrUnverified
		}
		if name == executable {
			executableDigest = hex.EncodeToString(hash.Sum(nil))
			executableSize = size
		}
		return nil
	}
	if isZip {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil || len(z.File) > maxEntries {
			return "", 0, ErrUnverified
		}
		for _, f := range z.File {
			dir := f.FileInfo().IsDir()
			if (!dir && !f.Mode().IsRegular()) || checkName(f.Name, dir) != nil || (dir && f.UncompressedSize64 != 0) || f.UncompressedSize64 > maxExpandedBytes {
				return "", 0, ErrUnverified
			}
			if dir {
				continue
			}
			r, err := f.Open()
			if err != nil {
				return "", 0, ErrUnverified
			}
			err = consume(f.Name, int64(f.UncompressedSize64), r)
			closeErr := r.Close()
			if err != nil || closeErr != nil {
				return "", 0, ErrUnverified
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
			dir := h.Typeflag == tar.TypeDir
			if (!dir && h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA) || checkName(h.Name, dir) != nil || (dir && h.Size != 0) {
				return "", 0, ErrUnverified
			}
			if !dir && consume(h.Name, h.Size, t) != nil {
				return "", 0, ErrUnverified
			}
		}
		// Consume the gzip trailer as well; malformed streams cannot verify.
		if _, err := io.Copy(io.Discard, expanded); err != nil || expanded.N <= 0 {
			return "", 0, ErrUnverified
		}
	}
	if executableDigest == "" || ctx.Err() != nil {
		return "", 0, ErrUnverified
	}
	return executableDigest, executableSize, nil
}
