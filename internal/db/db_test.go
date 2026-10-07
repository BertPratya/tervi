package db_test

import (
	"context"
	"testing"

	"github.com/bertpratya/tervi/internal/db"
	"github.com/bertpratya/tervi/internal/db/dbtest"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrateCreatesTables(t *testing.T) {
	pool := dbtest.New(t)
	wantColumns := map[string][]string{
		"pairing_requests": {
			"id", "hostname", "os_name", "os_version", "status", "polling_key_hash", "approval_key_hash",
			"pairing_code", "tries_left", "failure_reason", "created_at", "expires_at",
		},
		"machines": {
			"machine_id", "pairing_request_id", "hostname", "os_name", "os_version", "display_name",
			"credential_hash", "status", "created_at", "expires_at",
		},
	}
	for table, columns := range wantColumns {
		for _, column := range columns {
			var exists bool
			err := pool.QueryRow(context.Background(), `SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2
			)`, table, column).Scan(&exists)
			if err != nil {
				t.Fatalf("check %s.%s: %v", table, column, err)
			}
			if !exists {
				t.Errorf("column %s.%s does not exist", table, column)
			}
		}
	}
}

func TestMigrateIsRepeatable(t *testing.T) {
	pool := dbtest.New(t)
	var before int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM goose_db_version`).Scan(&before); err != nil {
		t.Fatalf("count migration records before second run: %v", err)
	}
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	var after int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM goose_db_version`).Scan(&after); err != nil {
		t.Fatalf("count migration records after second run: %v", err)
	}
	if after != before {
		t.Fatalf("migration record count after second run = %d, before = %d", after, before)
	}
}

func TestHashColumnsAreUnique(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	firstID := insertRequest(t, pool, []byte("polling-one"), []byte("approval-one"))
	if _, err := pool.Exec(ctx, `INSERT INTO machines
		(pairing_request_id, display_name, credential_hash, status, expires_at)
		VALUES ($1, 'first', $2, 'active', now() + interval '10 minutes')`, firstID, []byte("credential-one")); err != nil {
		t.Fatalf("insert initial machine: %v", err)
	}

	secondID := insertRequest(t, pool, []byte("polling-two"), []byte("approval-two"))
	checks := []struct {
		name string
		query string
		args []any
	}{
		{
			name: "polling key hash",
			query: `INSERT INTO pairing_requests (status, polling_key_hash, approval_key_hash, expires_at)
				VALUES ('waiting_for_approval', $1, $2, now() + interval '10 minutes')`,
			args: []any{[]byte("polling-one"), []byte("approval-three")},
		},
		{
			name: "approval key hash",
			query: `INSERT INTO pairing_requests (status, polling_key_hash, approval_key_hash, expires_at)
				VALUES ('waiting_for_approval', $1, $2, now() + interval '10 minutes')`,
			args: []any{[]byte("polling-three"), []byte("approval-one")},
		},
		{
			name: "credential hash",
			query: `INSERT INTO machines (pairing_request_id, display_name, credential_hash, status, expires_at)
				VALUES ($1, 'second', $2, 'active', now() + interval '10 minutes')`,
			args: []any{secondID, []byte("credential-one")},
		},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, check.query, check.args...); err == nil {
				t.Fatalf("insert with duplicate %s succeeded", check.name)
			}
		})
	}
}

func TestStatusCheck(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO pairing_requests
		(status, polling_key_hash, approval_key_hash, expires_at)
		VALUES ('impossible', $1, $2, now() + interval '10 minutes')`, []byte("p"), []byte("a")); err == nil {
		t.Fatal("pairing_requests accepted an unknown status")
	}
	id := insertRequest(t, pool, []byte("p2"), []byte("a2"))
	if _, err := pool.Exec(ctx, `INSERT INTO machines
		(pairing_request_id, display_name, credential_hash, status, expires_at)
		VALUES ($1, 'machine', $2, 'impossible', now() + interval '10 minutes')`, id, []byte("credential")); err == nil {
		t.Fatal("machines accepted an unknown status")
	}
}

func insertRequest(t *testing.T, pool *pgxpool.Pool, pollingHash, approvalHash []byte) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `INSERT INTO pairing_requests
		(status, polling_key_hash, approval_key_hash, expires_at)
		VALUES ('waiting_for_approval', $1, $2, now() + interval '10 minutes')
		RETURNING id::text`, pollingHash, approvalHash).Scan(&id)
	if err != nil {
		t.Fatalf("insert pairing request: %v", err)
	}
	return id
}
