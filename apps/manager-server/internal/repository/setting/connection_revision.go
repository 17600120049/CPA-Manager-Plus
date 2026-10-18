package setting

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

// Persisted CPA connection revisions are private Manager-owned facts.
// They do not attest to the identity of the CPA instance behind the URL
// and must never authorize a plugin without separate effective-source checks.
const persistedCPAConnectionRevisionKey = "cpa_connection_revision_v1"

type persistedConnectionSnapshot struct {
	baseURL  string
	key      string
	revision string
	valid    bool
}

func (s persistedConnectionSnapshot) sameConnection(other persistedConnectionSnapshot) bool {
	return s.valid && other.valid && s.baseURL == other.baseURL && s.key == other.key
}

// LoadPersistedCPAConnectionRevision intentionally does not accept env-owned
// connections as qualified. A future consumer MUST resolve effective ownership
// separately; this method describes the persisted DB pair only.
func (r *repository) LoadPersistedCPAConnectionRevision(ctx context.Context) (string, bool, error) {
	// Read the manager row, legacy setup and revision in one DB snapshot.
	// Separate autocommit reads could combine different committed epochs.
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback() }()
	snapshot, err := r.readConnectionSnapshot(ctx, tx)
	if err != nil {
		return "", false, err
	}
	if !snapshot.valid || snapshot.revision == "" {
		return "", false, nil
	}
	return snapshot.revision, true, nil
}

type connectionSnapshotQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readConnectionSetting(ctx context.Context, db connectionSnapshotQueryer, key string) (string, bool, error) {
	var raw string
	err := db.QueryRowContext(ctx, "select value from settings where key = ?", key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return raw, err == nil, err
}

func (r *repository) readConnectionSnapshot(ctx context.Context, db connectionSnapshotQueryer) (persistedConnectionSnapshot, error) {
	var snapshot persistedConnectionSnapshot
	cfgRaw, cfgOK, err := readConnectionSetting(ctx, db, managerConfigKey)
	if err != nil || !cfgOK {
		return snapshot, err
	}
	setupRaw, setupOK, err := readConnectionSetting(ctx, db, "setup")
	if err != nil || !setupOK {
		return snapshot, err
	}

	var cfg model.ManagerConfig
	var setup model.Setup
	if json.Unmarshal([]byte(cfgRaw), &cfg) != nil || json.Unmarshal([]byte(setupRaw), &setup) != nil {
		return snapshot, nil // malformed history is unqualified; existing writes may repair it
	}
	cfg, err = r.unprotectManagerConfig(cfg)
	if err != nil {
		return snapshot, nil
	}
	setup, err = r.unprotectSetup(setup)
	if err != nil {
		return snapshot, nil
	}
	base := strings.TrimSpace(cfg.CPAConnection.CPABaseURL)
	key := cfg.CPAConnection.ManagementKey
	if base == "" || strings.TrimSpace(key) == "" ||
		base != strings.TrimSpace(setup.CPAUpstreamURL) ||
		key != setup.ManagementKey {
		return snapshot, nil
	}
	snapshot.valid = true
	snapshot.baseURL = base
	snapshot.key = key
	raw, exists, err := readConnectionSetting(ctx, db, persistedCPAConnectionRevisionKey)
	if err != nil {
		return persistedConnectionSnapshot{}, err
	}
	if exists {
		// Only a syntactically valid opaque random revision can be reused.
		if v, decodeErr := hex.DecodeString(raw); decodeErr == nil && len(v) == 16 {
			snapshot.revision = raw
		}
	}
	return snapshot, nil
}

// saveWithConnectionRevision serializes the source write and revision update
// in the same SQLite transaction. Old or ambiguous sources are never upgraded
// simply by reading them. Legacy normalization can preserve an existing valid
// revision but is not an authorization-bearing producer.
func (r *repository) saveWithConnectionRevision(
	ctx context.Context,
	allowNewRevision bool,
	write func(*sql.Tx) error,
) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Claim the SQLite writer before reading the previous authority. A deferred
	// read transaction can otherwise conflict with another connection writer
	// when both readers later try to upgrade to a writer.
	if _, err := tx.ExecContext(ctx, "update settings set value = value where key = ?", managerConfigKey); err != nil {
		return err
	}
	before, err := r.readConnectionSnapshot(ctx, tx)
	if err != nil {
		return err
	}
	if err := write(tx); err != nil {
		return err
	}
	after, err := r.readConnectionSnapshot(ctx, tx)
	if err != nil {
		return err
	}

	if !after.valid || (!allowNewRevision && (!before.sameConnection(after) || before.revision == "")) {
		if _, err := tx.ExecContext(ctx, "delete from settings where key = ?", persistedCPAConnectionRevisionKey); err != nil {
			return err
		}
	} else if !before.sameConnection(after) || before.revision == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return err
		}
		if err := upsertSetting(ctx, tx, persistedCPAConnectionRevisionKey, []byte(hex.EncodeToString(random[:])), time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
