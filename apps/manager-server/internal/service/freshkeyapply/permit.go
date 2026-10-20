// Package freshkeyapply owns the bounded, private S1 initial-install consumer.
// The host launcher owns isolation evidence; neither Admin JSON nor an environment
// boolean is proof. No runtime model admission or general config writing exists.
package freshkeyapply

import (
	"encoding/json"
	"io"
	"os"
	"runtime"
	"time"
)

const PermitPath = "/run/cpamp-s1/permit.json"

// Permit is issued only by the serial disposable Docker launcher, mounted
// read-only into Manager after host verification. Host root/Docker remain trusted.
// Its management secret is ephemeral host provisioning, never a Client Key store.
type Permit struct {
	RunID         string `json:"runId"`
	Generation    string `json:"generation"`
	ManagementKey string `json:"managementKey"`
	IssuedAtMS    int64  `json:"issuedAtMs"`
	ExpiresAtMS   int64  `json:"expiresAtMs"`
}

func loadPermit() (Permit, error) {
	var p Permit
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return p, ErrUnavailable
	}
	f, err := os.Open(PermitPath)
	if err != nil {
		return p, ErrUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
		return p, ErrUnavailable
	}
	if !rootOwned(info) {
		return p, ErrUnavailable
	}
	// The launcher uses a dedicated volume; reject symlink substitution too.
	link, err := os.Lstat(PermitPath)
	if err != nil || !os.SameFile(info, link) || link.Mode()&os.ModeSymlink != 0 {
		return p, ErrUnavailable
	}
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(&struct{}{}) != io.EOF || !p.valid(time.Now()) {
		return Permit{}, ErrUnavailable
	}
	return p, nil
}

func (p Permit) valid(now time.Time) bool {
	n := now.UnixMilli()
	return len(p.RunID) >= 16 && p.Generation != "" && len(p.ManagementKey) == 64 && p.IssuedAtMS > 0 && p.IssuedAtMS <= n && p.ExpiresAtMS > n && p.ExpiresAtMS-p.IssuedAtMS <= 600000
}
