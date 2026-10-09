package dbtest

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDatabaseURLFor(t *testing.T) {
	tests := []struct {
		name        string
		databaseURL string
		wantSSLMode string
	}{
		{
			name:        "plain URL",
			databaseURL: "postgres://user:pass@localhost/original",
		},
		{
			name:        "dbname query parameter",
			databaseURL: "postgres://user:pass@localhost/original?dbname=tervi&sslmode=disable",
			wantSSLMode: "disable",
		},
		{
			name:        "database query parameter",
			databaseURL: "postgres://user:pass@localhost/original?database=tervi",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const wantDatabase = "tervi_test"
			got, err := databaseURLFor(tt.databaseURL, wantDatabase)
			if err != nil {
				t.Fatalf("databaseURLFor() error = %v", err)
			}
			config, err := pgx.ParseConfig(got)
			if err != nil {
				t.Fatalf("parse returned database URL: %v", err)
			}
			if config.Database != wantDatabase {
				t.Fatalf("parsed database = %q, want %q", config.Database, wantDatabase)
			}
			if tt.wantSSLMode != "" {
				parsed, err := url.Parse(got)
				if err != nil {
					t.Fatalf("parse returned URL: %v", err)
				}
				if gotSSLMode := parsed.Query().Get("sslmode"); gotSSLMode != tt.wantSSLMode {
					t.Fatalf("sslmode query parameter = %q, want %q", gotSSLMode, tt.wantSSLMode)
				}
			}
		})
	}
}

func TestDBTestFindsURL(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "internal", "db", "dbtest")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DATABASE_URL=from-env-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte("DATABASE_URL=from-example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	moduleRoot, err := findModuleRoot(nested)
	if err != nil {
		t.Fatalf("findModuleRoot() error = %v", err)
	}
	if moduleRoot != root {
		t.Fatalf("module root = %q, want %q", moduleRoot, root)
	}

	lookup := func(value string, present bool) func(string) (string, bool) {
		return func(key string) (string, bool) {
			return value, key == "DATABASE_URL" && present
		}
	}
	if got, err := findDatabaseURL(moduleRoot, lookup("from-process", true)); err != nil || got != "from-process" {
		t.Fatalf("findDatabaseURL() process = %q, %v; want from-process", got, err)
	}
	if got, err := findDatabaseURL(moduleRoot, lookup("", false)); err != nil || got != "from-env-file" {
		t.Fatalf("findDatabaseURL() .env = %q, %v; want from-env-file", got, err)
	}
	if err := os.Remove(filepath.Join(root, ".env")); err != nil {
		t.Fatal(err)
	}
	if got, err := findDatabaseURL(moduleRoot, lookup("", false)); err != nil || got != "from-example" {
		t.Fatalf("findDatabaseURL() .env.example = %q, %v; want from-example", got, err)
	}
}

func TestDBTestIsolated(t *testing.T) {
	moduleRoot, err := findModuleRootFromWorkingDirectory()
	if err != nil {
		t.Fatalf("find module root: %v", err)
	}
	databaseURL, err := findDatabaseURL(moduleRoot, os.LookupEnv)
	if err != nil {
		t.Fatalf("find DATABASE_URL: %v", err)
	}
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	adminConfig.ConnConfig.Database = "postgres"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	adminPool, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect to PostgreSQL host %q: %v", adminConfig.ConnConfig.Host, err)
	}
	defer adminPool.Close()
	if err := adminPool.Ping(ctx); err != nil {
		t.Fatalf("cannot reach PostgreSQL host %q from DATABASE_URL", adminConfig.ConnConfig.Host)
	}

	var dbNames []string
	t.Run("isolated databases", func(t *testing.T) {
		first := New(t)
		second := New(t)
		dbNames = []string{first.Config().ConnConfig.Database, second.Config().ConnConfig.Database}
		if dbNames[0] == dbNames[1] {
			t.Fatalf("both pools use database %q", dbNames[0])
		}

		_, err := first.Exec(ctx, `INSERT INTO pairing_requests
			(status, polling_key_hash, approval_key_hash, expires_at)
			VALUES ('waiting_for_approval', $1, $2, now() + interval '10 minutes')`,
			[]byte("polling-hash"), []byte("approval-hash"))
		if err != nil {
			t.Fatalf("insert into first database: %v", err)
		}
		var count int
		if err := second.QueryRow(ctx, `SELECT count(*) FROM pairing_requests`).Scan(&count); err != nil {
			t.Fatalf("query second database: %v", err)
		}
		if count != 0 {
			t.Fatalf("row count in second database = %d, want 0", count)
		}
	})

	for _, name := range dbNames {
		var exists bool
		if err := adminPool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
			t.Fatalf("check whether database %q remains: %v", name, err)
		}
		if exists {
			t.Errorf("test database %q still exists after cleanup", name)
		}
	}
}
