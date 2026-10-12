package maintenancebundle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Check every existing ancestor, including symlinks above the archive root.
// This assumes the caller exclusively owns placement; it is not an openat
// sandbox against a hostile same-user process racing directory replacements.
func safePath(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("Manager bundle paths cannot contain symbolic links")
			}
			if current != path && !info.IsDir() {
				return errors.New("Manager bundle ancestor is not a directory")
			}
		}
		if current == filepath.Dir(current) {
			return nil
		}
	}
}

func digestFile(ctx context.Context, path string, dst io.Writer) (Entry, error) {
	if err := safePath(path); err != nil {
		return Entry{}, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return Entry{}, err
	}
	if !before.Mode().IsRegular() {
		return Entry{}, errors.New("Manager bundle file is not regular")
	}
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return Entry{}, err
	}
	if !os.SameFile(before, opened) {
		return Entry{}, errors.New("Manager bundle file changed while opening")
	}
	if err := rejectMultipleLinks(f); err != nil {
		return Entry{}, err
	}
	h := sha256.New()
	var out io.Writer = h
	if dst != nil {
		out = io.MultiWriter(h, dst)
	}
	n, err := copyContext(ctx, out, f)
	if err != nil {
		return Entry{}, err
	}
	after, err := f.Stat()
	if err != nil {
		return Entry{}, err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return Entry{}, err
	}
	if !os.SameFile(opened, current) || after.Size() != opened.Size() || n != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return Entry{}, errors.New("Manager bundle file changed while reading")
	}
	return Entry{Present: true, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 128*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			written, err := dst.Write(buffer[:n])
			total += int64(written)
			if err != nil {
				return total, err
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func copyFile(ctx context.Context, source, target string) error {
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = digestFile(ctx, source, f)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

func writeFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
