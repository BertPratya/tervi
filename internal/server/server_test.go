package server_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/server"
	"github.com/bertpratya/tervi/internal/server/db"
	"github.com/bertpratya/tervi/internal/server/db/dbtest"
)

func TestHealth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Host = "localhost"
	rec := httptest.NewRecorder()

	server.New(server.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); body != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestHostCheck(t *testing.T) {
	handler := server.New(server.Config{PublicURL: "http://tervi.test:8080"}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for _, host := range []string{"localhost:8080", "LOCALHOST:8080", "127.0.0.1:9000", "[::1]:8080", "tervi.test"} {
		t.Run("allowed_"+host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			req.Host = host
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
			}
			if body := rec.Body.String(); body != "ok" {
				t.Errorf("body = %q, want %q", body, "ok")
			}
		})
	}

	for _, host := range []string{"evil.example:8080", "example.com", "127.0.0.1.evil.example", ""} {
		t.Run("rejected_"+host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/not-a-route", nil)
			req.Host = host
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d; body = %q", rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want %q", got, "application/json")
			}
			if body := rec.Body.String(); body != `{"error": "unknown_host"}` {
				t.Errorf("body = %q, want %q", body, `{"error": "unknown_host"}`)
			}
		})
	}
}

func TestRunRefusesUnmigratedDatabase(t *testing.T) {
	pool, databaseURL := dbtest.NewEmpty(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release address: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = server.Run(ctx, server.Config{ServerAddr: address, PublicURL: "http://localhost", DatabaseURL: databaseURL}, nil)
	if !errors.Is(err, db.ErrNotMigrated) {
		t.Fatalf("Run() error = %v, want ErrNotMigrated", err)
	}
	conn, dialErr := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		t.Fatalf("server accepted connections at %s", address)
	}
	var tableCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema()`).Scan(&tableCount); err != nil {
		t.Fatalf("count tables in empty database: %v", err)
	}
	if tableCount != 0 {
		t.Fatalf("Run() created %d tables in an unmigrated database", tableCount)
	}
}
