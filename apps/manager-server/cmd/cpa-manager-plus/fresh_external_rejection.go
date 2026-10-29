package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/freshconfig"
)

// externalRejectionKey is the one settings row that records that the user's CPA
// refused the stored External management credential. It exists because CPA bans
// the calling address for thirty minutes after five failed authentications: a page
// that keeps presenting a credential its own CPA already rejected is what turns a
// rotation into an outage for the person trying to fix it.
const externalRejectionKey = "fresh_external_rejection_v1"

// externalRejectionRecord is the refused connection and when it was refused. The
// address digest is the identity the whole External profile is keyed by, so a
// rebind to another CPA can never inherit this refusal.
type externalRejectionRecord struct {
	Identity string `json:"identity"`
	AtMS     int64  `json:"atMs"`
}

// externalRejection is the durable refusal of the stored External credential.
// Every browser-driven read of the user's CPA consults it before it sends
// anything, so a refused key is answered as the typed blocker instead of being
// presented once more. Only a rebind, or a re-verify that proves the stored key
// works again, clears it.
type externalRejection struct {
	db *sql.DB
	mu sync.Mutex
}

func newExternalRejection(db *sql.DB) *externalRejection {
	return &externalRejection{db: db}
}

// refused reports whether the CPA has refused the credential of exactly this
// connection. An unreadable row is not a refusal: this Manager failing to read its
// own note must not hide the user's data, which is what the read is for.
func (r *externalRejection) refused(ctx context.Context, identity string) bool {
	record, ok := r.load(ctx)
	return ok && record.Identity == identity
}

func (r *externalRejection) load(ctx context.Context) (externalRejectionRecord, bool) {
	if r == nil || r.db == nil {
		return externalRejectionRecord{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var raw string
	if err := r.db.QueryRowContext(ctx, `select value from settings where key = ?`, externalRejectionKey).Scan(&raw); err != nil {
		return externalRejectionRecord{}, false
	}
	var record externalRejectionRecord
	if json.Unmarshal([]byte(raw), &record) != nil || record.Identity == "" {
		return externalRejectionRecord{}, false
	}
	return record, true
}

// record marks this connection refused. Failing to write the row is not a blocker
// of the read that met the refusal: that read still answers with the typed code,
// and the next one simply pays one more request.
func (r *externalRejection) record(ctx context.Context, identity string) {
	if r == nil || r.db == nil || identity == "" {
		return
	}
	raw, err := json.Marshal(externalRejectionRecord{Identity: identity, AtMS: time.Now().UnixMilli()})
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.db.ExecContext(ctx,
		`insert into settings(key,value,updated_at_ms) values(?,?,?) on conflict(key) do update set value=excluded.value, updated_at_ms=excluded.updated_at_ms`,
		externalRejectionKey, string(raw), time.Now().UnixMilli())
}

// clear forgets the refusal. It runs only behind proof that the stored credential
// authenticates again: the successful read of a rebind or of a same-key re-verify.
func (r *externalRejection) clear(ctx context.Context) {
	if r == nil || r.db == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.db.ExecContext(ctx, `delete from settings where key = ?`, externalRejectionKey)
}

// externalReadRefused reports whether the user's CPA has already refused the stored
// credential of this committed connection. A browser-driven read asks this before
// it sends anything: the CPA would answer 401/403 again, and five of those ban the
// address for thirty minutes.
func (a *freshApplication) externalReadRefused(ctx context.Context) bool {
	if a == nil || a.rejection == nil || a.externalSource == nil {
		return false
	}
	install, err := a.externalSource(ctx)
	if err != nil {
		// An unresolvable authority is its own typed blocker further down; it is
		// not evidence that the credential was refused.
		return false
	}
	return a.rejection.refused(ctx, install.Identity)
}

// externalReadRefusedError is the typed blocker a refused read answers with
// without contacting the CPA.
func externalReadRefusedError() error { return cpa.ErrManagementRejected }

// writeExternalManagementRejected answers the one wire shape the browser treats as
// a refused credential: a conflict with the typed code, which offers a rebind and
// no retry.
func writeExternalManagementRejected(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	writeFreshJSON(w, struct {
		Code string `json:"code"`
	}{freshconfig.ReasonManagementRejected})
}

// noteExternalRejection records a refusal of the stored credential. It runs on the
// error mapping of every browser-driven read, which is where a CPA refusal is
// first known. A platform error, a moved target and a version refusal are not
// refusals of the credential and record nothing.
func (a *freshApplication) noteExternalRejection(ctx context.Context, err error) {
	if a == nil || a.rejection == nil || a.externalSource == nil || err == nil {
		return
	}
	if !errors.Is(err, cpa.ErrManagementRejected) {
		return
	}
	install, resolveErr := a.externalSource(ctx)
	if resolveErr != nil {
		// The refusal is about a connection this Manager cannot even name, so
		// there is no identity to record it against.
		return
	}
	a.rejection.record(ctx, install.Identity)
}

// clearExternalRejection forgets a recorded refusal after a read proved that the
// stored credential authenticates again.
func (a *freshApplication) clearExternalRejection(ctx context.Context) {
	if a == nil || a.rejection == nil {
		return
	}
	a.rejection.clear(ctx)
}
