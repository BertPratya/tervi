package db_test

import (
	"context"
	"io/fs"
	"os"
	"testing"

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
	if databaseVersion != 2 || newestVersion != 2 {
		t.Fatalf("migration versions = (%d, %d), want (2, 2)", databaseVersion, newestVersion)
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
