package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bertpratya/tervi/internal/server/db"
	"github.com/bertpratya/tervi/internal/server/db/dbtest"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrateCreatesTables(t *testing.T) {
	pool := dbtest.New(t)
	type columnSpec struct {
		dataType   string
		isNullable string
	}
	wantColumns := map[string]map[string]columnSpec{
		"pairing_requests": {
			"id":                 {dataType: "uuid", isNullable: "NO"},
			"hostname":           {dataType: "text", isNullable: "NO"},
			"os_name":            {dataType: "text", isNullable: "NO"},
			"os_version":         {dataType: "text", isNullable: "NO"},
			"status":             {dataType: "text", isNullable: "NO"},
			"polling_key_hash":   {dataType: "bytea", isNullable: "NO"},
			"approval_key_hash":  {dataType: "bytea", isNullable: "NO"},
			"pairing_code":       {dataType: "text", isNullable: "YES"},
			"tries_left":         {dataType: "integer", isNullable: "NO"},
			"failure_reason":     {dataType: "text", isNullable: "YES"},
			"created_at":         {dataType: "timestamp with time zone", isNullable: "NO"},
			"expires_at":         {dataType: "timestamp with time zone", isNullable: "NO"},
			"identity_hash":      {dataType: "bytea", isNullable: "YES"},
			"matched_machine_id": {dataType: "uuid", isNullable: "YES"},
		},
		"machines": {
			"machine_id":    {dataType: "uuid", isNullable: "NO"},
			"hostname":      {dataType: "text", isNullable: "NO"},
			"os_name":       {dataType: "text", isNullable: "NO"},
			"os_version":    {dataType: "text", isNullable: "NO"},
			"display_name":  {dataType: "text", isNullable: "NO"},
			"created_at":    {dataType: "timestamp with time zone", isNullable: "NO"},
			"last_seen_at":  {dataType: "timestamp with time zone", isNullable: "YES"},
			"identity_hash": {dataType: "bytea", isNullable: "YES"},
		},
		"credentials": {
			"credential_id":      {dataType: "uuid", isNullable: "NO"},
			"machine_id":         {dataType: "uuid", isNullable: "NO"},
			"pairing_request_id": {dataType: "uuid", isNullable: "NO"},
			"credential_hash":    {dataType: "bytea", isNullable: "NO"},
			"status":             {dataType: "text", isNullable: "NO"},
			"created_at":         {dataType: "timestamp with time zone", isNullable: "NO"},
			"expires_at":         {dataType: "timestamp with time zone", isNullable: "NO"},
			"ended_at":           {dataType: "timestamp with time zone", isNullable: "YES"},
		},
	}
	for table, columns := range wantColumns {
		for column, want := range columns {
			var got columnSpec
			err := pool.QueryRow(context.Background(), `SELECT data_type, is_nullable
				FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`, table, column).
				Scan(&got.dataType, &got.isNullable)
			if err != nil {
				t.Fatalf("check %s.%s: %v", table, column, err)
			}
			if got != want {
				t.Errorf("column %s.%s = (%s, nullable %s), want (%s, nullable %s)", table, column, got.dataType, got.isNullable, want.dataType, want.isNullable)
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
	if _, err := db.Migrate(context.Background(), pool); err != nil {
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
	var firstMachineID string
	if err := pool.QueryRow(ctx, `INSERT INTO machines (display_name) VALUES ('first') RETURNING machine_id::text`).Scan(&firstMachineID); err != nil {
		t.Fatalf("insert initial machine: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO credentials
		(machine_id, pairing_request_id, credential_hash, status, expires_at)
		VALUES ($1, $2, $3, 'active', now() + interval '10 minutes')`, firstMachineID, firstID, []byte("credential-one")); err != nil {
		t.Fatalf("insert initial credential: %v", err)
	}

	secondID := insertRequest(t, pool, []byte("polling-two"), []byte("approval-two"))
	var secondMachineID string
	if err := pool.QueryRow(ctx, `INSERT INTO machines (display_name) VALUES ('second') RETURNING machine_id::text`).Scan(&secondMachineID); err != nil {
		t.Fatalf("insert second machine: %v", err)
	}
	checks := []struct {
		name  string
		query string
		args  []any
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
			query: `INSERT INTO credentials (machine_id, pairing_request_id, credential_hash, status, expires_at)
				VALUES ($1, $2, $3, 'active', now() + interval '10 minutes')`,
			args: []any{secondMachineID, secondID, []byte("credential-one")},
		},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, check.query, check.args...)
			if err == nil {
				t.Fatalf("insert with duplicate %s succeeded", check.name)
			}
			assertPostgresErrorCode(t, err, "23505")
		})
	}
}

func TestMigrationOneActivePerMachine(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	requestID := insertRequest(t, pool, []byte("active-machine-poll"), []byte("active-machine-approval"))
	var machineID string
	if err := pool.QueryRow(ctx, `INSERT INTO machines (display_name)
		VALUES ('one active credential machine') RETURNING machine_id::text`).Scan(&machineID); err != nil {
		t.Fatalf("insert machine: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO credentials
		(machine_id, pairing_request_id, credential_hash, status, expires_at)
		VALUES ($1, $2, $3, 'active', now() + interval '10 minutes')`, machineID, requestID, []byte("first-active-hash")); err != nil {
		t.Fatalf("insert first active credential: %v", err)
	}
	secondRequestID := insertRequest(t, pool, []byte("active-machine-poll-2"), []byte("active-machine-approval-2"))
	_, err := pool.Exec(ctx, `INSERT INTO credentials
		(machine_id, pairing_request_id, credential_hash, status, expires_at)
		VALUES ($1, $2, $3, 'active', now() + interval '10 minutes')`, machineID, secondRequestID, []byte("second-active-hash"))
	if err == nil {
		t.Fatal("database accepted a second active credential for one machine")
	}
	assertPostgresErrorCode(t, err, "23505")

	thirdRequestID := insertRequest(t, pool, []byte("revoked-machine-poll"), []byte("revoked-machine-approval"))
	_, err = pool.Exec(ctx, `INSERT INTO credentials
		(machine_id, pairing_request_id, credential_hash, status, expires_at)
		VALUES ($1, $2, $3, 'revoked', now() + interval '10 minutes')`, machineID, thirdRequestID, []byte("invalid-revoked-hash"))
	if err == nil {
		t.Fatal("database accepted a revoked credential without ended_at")
	}
	assertPostgresErrorCode(t, err, "23514")
}

func TestStatusCheck(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO pairing_requests
		(status, polling_key_hash, approval_key_hash, expires_at)
		VALUES ('impossible', $1, $2, now() + interval '10 minutes')`, []byte("p"), []byte("a"))
	if err == nil {
		t.Fatal("pairing_requests accepted an unknown status")
	}
	assertPostgresErrorCode(t, err, "23514")
	id := insertRequest(t, pool, []byte("p2"), []byte("a2"))
	var machineID string
	if err := pool.QueryRow(ctx, `INSERT INTO machines (display_name) VALUES ('machine') RETURNING machine_id::text`).Scan(&machineID); err != nil {
		t.Fatalf("insert machine: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO credentials
		(machine_id, pairing_request_id, credential_hash, status, expires_at)
		VALUES ($1, $2, $3, 'impossible', now() + interval '10 minutes')`, machineID, id, []byte("credential"))
	if err == nil {
		t.Fatal("credentials accepted an unknown status")
	}
	assertPostgresErrorCode(t, err, "23514")
}

func assertPostgresErrorCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error = %v, want PostgreSQL error code %s", err, wantCode)
	}
	if pgErr.Code != wantCode {
		t.Fatalf("PostgreSQL error code = %s, want %s (error: %v)", pgErr.Code, wantCode, err)
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
