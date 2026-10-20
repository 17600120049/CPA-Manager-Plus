package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestFreshKeyNamesUpgradeExistingStagedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-rename.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`create table gateway_api_key_identities(id text primary key,revision integer not null,lifecycle text not null,created_at_ms integer not null,updated_at_ms integer not null)`,
		`create table fresh_client_api_keys(request_id text primary key,api_key_id text not null unique references gateway_api_key_identities(id),secret_ciphertext text not null,secret_hash text not null unique,masked_preview text not null)`,
		`insert into gateway_api_key_identities values('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',1,'active',1,1)`,
		`insert into fresh_client_api_keys values('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','existing ciphertext','existing hash','existing preview')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		db, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var name, cipher, hash, preview, id string
		var revision int64
		if err := db.QueryRow(`select api_key_id,secret_ciphertext,secret_hash,masked_preview,name,name_revision from fresh_client_api_keys`).Scan(&id, &cipher, &hash, &preview, &name, &revision); err != nil {
			t.Fatal(err)
		}
		if id != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || cipher != "existing ciphertext" || hash != "existing hash" || preview != "existing preview" || name != "" || revision != 1 {
			t.Fatal("upgrade changed existing key record")
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
