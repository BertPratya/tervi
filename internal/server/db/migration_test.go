package db_test

import (
	"context"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/server/db/dbtest"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestMigrationAddsLastSeen(t *testing.T) {
	ctx := context.Background()
	pool, _ := dbtest.NewEmpty(t)
	database := stdlib.OpenDB(*pool.Config().ConnConfig)
	t.Cleanup(func() { _ = database.Close() })
	migrations, err := fs.Sub(os.DirFS("."), "migrations")
	if err != nil {
		t.Fatalf("open migration directory: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, database, migrations, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("apply initial migration: %v", err)
	}
	var pairingID string
	if err := pool.QueryRow(ctx, `INSERT INTO pairing_requests
		(status, polling_key_hash, approval_key_hash, expires_at)
		VALUES ('waiting_for_approval', $1, $2, now() + interval '10 minutes') RETURNING id::text`,
		[]byte("polling-before-last-seen"), []byte("approval-before-last-seen")).Scan(&pairingID); err != nil {
		t.Fatalf("insert pairing before last_seen migration: %v", err)
	}
	var machineID string
	if err := pool.QueryRow(ctx, `INSERT INTO machines
		(pairing_request_id, display_name, credential_hash, status, expires_at)
		VALUES ($1, 'existing machine', $2, 'active', now() + interval '10 minutes') RETURNING machine_id::text`,
		pairingID, []byte("existing-machine-hash")).Scan(&machineID); err != nil {
		t.Fatalf("insert machine before last_seen migration: %v", err)
	}
	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("apply last_seen migration: %v", err)
	}
	databaseVersion, newestVersion, err := provider.GetVersions(ctx)
	if err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if databaseVersion != 2 || newestVersion != 3 {
		t.Fatalf("migration versions = (%d, %d), want (2, 3)", databaseVersion, newestVersion)
	}
	var nullable string
	if err := pool.QueryRow(ctx, `SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'machines' AND column_name = 'last_seen_at'`).Scan(&nullable); err != nil {
		t.Fatalf("read last_seen_at nullability: %v", err)
	}
	if nullable != "YES" {
		t.Fatalf("last_seen_at nullable = %q, want YES", nullable)
	}
	var lastSeenIsNull bool
	if err := pool.QueryRow(ctx, `SELECT last_seen_at IS NULL FROM machines WHERE machine_id = $1`, machineID).Scan(&lastSeenIsNull); err != nil {
		t.Fatalf("read existing machine last_seen_at: %v", err)
	}
	if !lastSeenIsNull {
		t.Fatal("existing machine last_seen_at is not null after migration")
	}
}

func TestMigrationMovesCredentials(t *testing.T) {
	ctx := context.Background()
	pool, _ := dbtest.NewEmpty(t)
	database := stdlib.OpenDB(*pool.Config().ConnConfig)
	t.Cleanup(func() { _ = database.Close() })
	migrations, err := fs.Sub(os.DirFS("."), "migrations")
	if err != nil {
		t.Fatalf("open migration directory: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, database, migrations, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	for range 2 {
		if _, err := provider.UpByOne(ctx); err != nil {
			t.Fatalf("apply migration through version 2: %v", err)
		}
	}

	types := []string{"pending", "active", "failed", "expired"}
	wantHashes := make(map[string][]byte, len(types))
	wantRequests := make(map[string]string, len(types))
	wantCreated := make(map[string]time.Time, len(types))
	wantExpires := make(map[string]time.Time, len(types))
	for index, status := range types {
		requestID := insertRequest(t, pool, []byte("migration-poll-"+status), []byte("migration-approval-"+status))
		createdAt := time.Date(2025, time.January, index+1, 12, 0, 0, 0, time.UTC)
		expiresAt := createdAt.Add(5 * time.Minute)
		hash := []byte("credential-hash-" + status)
		if _, err := pool.Exec(ctx, `INSERT INTO machines
			(pairing_request_id, display_name, credential_hash, status, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, requestID, status+" machine", hash, status, createdAt, expiresAt); err != nil {
			t.Fatalf("insert version 2 %s machine: %v", status, err)
		}
		wantHashes[status] = hash
		wantRequests[status] = requestID
		wantCreated[status] = createdAt
		wantExpires[status] = expiresAt
	}
	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("apply credentials migration: %v", err)
	}
	databaseVersion, newestVersion, err := provider.GetVersions(ctx)
	if err != nil {
		t.Fatalf("read migration versions: %v", err)
	}
	if databaseVersion != 3 || newestVersion != 3 {
		t.Fatalf("migration versions = (%d, %d), want (3, 3)", databaseVersion, newestVersion)
	}
	for _, status := range types {
		var row struct {
			hash      []byte
			requestID string
			status    string
			createdAt time.Time
			expiresAt time.Time
			count     int
		}
		if err := pool.QueryRow(ctx, `SELECT credential_hash, pairing_request_id::text, status, created_at, expires_at,
			(SELECT count(*) FROM credentials c2 WHERE c2.machine_id = c.machine_id)
			FROM credentials c WHERE machine_id = (
				SELECT machine_id FROM machines WHERE display_name = $1
			)`, status+" machine").Scan(&row.hash, &row.requestID, &row.status, &row.createdAt, &row.expiresAt, &row.count); err != nil {
			t.Fatalf("read migrated %s credential: %v", status, err)
		}
		if row.status != status || row.requestID != wantRequests[status] || row.count != 1 ||
			string(row.hash) != string(wantHashes[status]) || !row.createdAt.Equal(wantCreated[status]) || !row.expiresAt.Equal(wantExpires[status]) {
			t.Errorf("migrated %s credential = %+v, want original values and one row", status, row)
		}
	}
	for _, column := range []string{"pairing_request_id", "credential_hash", "status", "expires_at"} {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'machines' AND column_name = $1`, column).Scan(&count); err != nil {
			t.Fatalf("check removed machines.%s: %v", column, err)
		}
		if count != 0 {
			t.Errorf("machines.%s still exists", column)
		}
	}
	var nullColumns int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM machines WHERE identity_hash IS NOT NULL) +
		(SELECT count(*) FROM pairing_requests WHERE identity_hash IS NOT NULL OR matched_machine_id IS NOT NULL)`).Scan(&nullColumns); err != nil {
		t.Fatalf("check new identity columns are null: %v", err)
	}
	if nullColumns != 0 {
		t.Fatalf("new identity columns have %d non-null values, want zero", nullColumns)
	}
}
