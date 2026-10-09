// Package dbtest provides an isolated PostgreSQL database for each database test.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/server/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// New creates a uniquely named database, migrates it, and drops it at test cleanup.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := newDatabase(t, false)
	if _, err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return pool
}

// NewEmpty creates a uniquely named database with no tables and drops it at test cleanup.
// Its URL points directly at that database.
func NewEmpty(t *testing.T) (pool *pgxpool.Pool, databaseURL string) {
	t.Helper()
	return newDatabase(t, true)
}

func newDatabase(t *testing.T, needURL bool) (*pgxpool.Pool, string) {
	t.Helper()
	moduleRoot, err := findModuleRootFromWorkingDirectory()
	if err != nil {
		t.Fatalf("find module root: %v", err)
	}
	databaseURL, err := findDatabaseURL(moduleRoot, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}

	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse DATABASE_URL")
	}
	host := adminConfig.ConnConfig.Host
	adminConfig.ConnConfig.Database = "postgres"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	adminPool, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("cannot connect to PostgreSQL host %q from DATABASE_URL: %v", host, err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatalf("cannot reach PostgreSQL host %q from DATABASE_URL: %v", host, err)
	}

	name, err := randomDatabaseName()
	if err != nil {
		adminPool.Close()
		t.Fatalf("create random test database name: %v", err)
	}
	testDatabaseURL := ""
	if needURL {
		testDatabaseURL, err = databaseURLFor(databaseURL, name)
		if err != nil {
			adminPool.Close()
			t.Fatal(err)
		}
	}
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		adminPool.Close()
		t.Fatalf("create test database: %v", err)
	}

	testConfig := adminConfig.Copy()
	testConfig.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		dropDatabase(adminPool, name)
		adminPool.Close()
		t.Fatalf("connect to test database on PostgreSQL host %q: %v", host, err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := adminPool.Exec(dropCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop test database: %v", err)
		}
		adminPool.Close()
	})

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("cannot reach PostgreSQL host %q for test database: %v", host, err)
	}
	return pool, testDatabaseURL
}

func databaseURLFor(databaseURL, name string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil || (!strings.EqualFold(parsed.Scheme, "postgres") && !strings.EqualFold(parsed.Scheme, "postgresql")) {
		return "", fmt.Errorf("DATABASE_URL must be a postgres:// URL to create an empty test database")
	}
	parsed.Path = "/" + name
	parsed.RawPath = ""
	query := parsed.Query()
	query.Del("dbname")
	query.Del("database")
	parsed.RawQuery = query.Encode()

	result := parsed.String()
	config, err := pgx.ParseConfig(result)
	if err != nil {
		return "", fmt.Errorf("parse test database URL")
	}
	if config.Database != name {
		return "", fmt.Errorf("test database URL selects database %q, want %q", config.Database, name)
	}
	return result, nil
}

func dropDatabase(pool *pgxpool.Pool, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = pool.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
}

func randomDatabaseName() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "tervi_test_" + hex.EncodeToString(random[:]), nil
}

func findModuleRootFromWorkingDirectory() (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return findModuleRoot(workingDirectory)
}

func findModuleRoot(start string) (string, error) {
	directory, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil && !info.IsDir() {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("could not find go.mod walking up from %q", start)
		}
		directory = parent
	}
}

func findDatabaseURL(moduleRoot string, lookupEnv func(string) (string, bool)) (string, error) {
	if value, ok := lookupEnv("DATABASE_URL"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}
	for _, filename := range []string{".env", ".env.example"} {
		path := filepath.Join(moduleRoot, filename)
		values, err := godotenv.Read(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		if value := strings.TrimSpace(values["DATABASE_URL"]); value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("DATABASE_URL not found in the environment, %s/.env, or %s/.env.example", moduleRoot, moduleRoot)
}
