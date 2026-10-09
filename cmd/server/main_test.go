package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bertpratya/tervi/internal/server"
	"github.com/bertpratya/tervi/internal/server/db/dbtest"
)

func TestHealth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Host = "localhost"
	rec := httptest.NewRecorder()

	server.New(server.Config{}, nil, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); body != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestMigrateCommand(t *testing.T) {
	pool, databaseURL := dbtest.NewEmpty(t)
	t.Setenv("DATABASE_URL", databaseURL)

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"migrate"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() = %d; stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	const want = "Database is up to date (version 1).\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	for _, table := range []string{"pairing_requests", "machines"} {
		var exists bool
		if err := pool.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s does not exist after migrate", table)
		}
	}
	var before int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM goose_db_version`).Scan(&before); err != nil {
		t.Fatalf("count migration rows before second call: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"migrate"}, &stdout, &stderr); code != 0 {
		t.Fatalf("second run() = %d; stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	if stdout.String() != want {
		t.Fatalf("second stdout = %q, want %q", stdout.String(), want)
	}
	var after int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM goose_db_version`).Scan(&after); err != nil {
		t.Fatalf("count migration rows after second call: %v", err)
	}
	if after != before {
		t.Fatalf("migration rows after second call = %d, before = %d", after, before)
	}
}

func TestMigrateNeedsOnlyDatabaseURL(t *testing.T) {
	_, databaseURL := dbtest.NewEmpty(t)
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("SERVER_ADDR", "0.0.0.0:8080")
	t.Setenv("TERVI_PUBLIC_URL", "not a url")

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"migrate"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() = %d; stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "Database is up to date (version 1).\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestMigrateErrorHidesURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:hunter2-test@127.0.0.1:1/tervi")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"migrate"}, &stdout, &stderr); code != 1 {
		t.Fatalf("run() = %d, want 1; stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "hunter2-test") {
		t.Fatalf("outputs expose DATABASE_URL password: stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("stderr is empty, want a logged migrate error")
	}
}

func TestUnknownArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"serve"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run() = %d, want 2; stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage: server [migrate]") {
		t.Fatalf("stderr = %q, want usage", stderr.String())
	}
}
