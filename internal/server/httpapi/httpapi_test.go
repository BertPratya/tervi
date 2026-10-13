package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestHTTPAPIHelpers(t *testing.T) {
	for _, authorization := range []struct {
		value string
		proof string
		ok    bool
	}{
		{value: "Bearer secret", proof: "secret", ok: true},
		{value: "bEaReR secret", proof: "secret", ok: true},
		{value: "bearer secret", proof: "secret", ok: true},
		{value: "Basic secret"},
		{value: "Bearer"},
		{value: "Bearer "},
		{value: "Bearer one two"},
		{value: "Bearer one\ttwo"},
	} {
		t.Run(authorization.value, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", authorization.value)
			proof, ok := BearerProof(req)
			if proof != authorization.proof || ok != authorization.ok {
				t.Fatalf("BearerProof() = (%q, %t), want (%q, %t)", proof, ok, authorization.proof, authorization.ok)
			}
		})
	}

	t.Run("WriteJSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteJSON(rec, http.StatusAccepted, map[string]string{"result": "ok"})
		if got := rec.Code; got != http.StatusAccepted {
			t.Errorf("status = %d, want %d", got, http.StatusAccepted)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		if got := strings.TrimSpace(rec.Body.String()); got != `{"result":"ok"}` {
			t.Errorf("body = %q, want JSON result", got)
		}
	})

	t.Run("WriteError", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteError(rec, http.StatusBadRequest, "invalid_input")
		if got := rec.Code; got != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", got, http.StatusBadRequest)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"invalid_input"}` {
			t.Errorf("body = %q, want JSON error", got)
		}
	})

	t.Run("WriteInternalError", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteInternalError(rec, nil, "read pairing", nil)
		if got := rec.Code; got != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", got, http.StatusInternalServerError)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"internal_error"}` {
			t.Errorf("body = %q, want internal_error JSON", got)
		}
	})
}

func TestInternalErrorLogsKindOnly(t *testing.T) {
	const marker = "secret-row-value-do-not-log"
	var log strings.Builder
	logger := slog.New(slog.NewTextHandler(&log, nil))
	WriteInternalError(httptest.NewRecorder(), logger, "read pairing", &pgconn.PgError{
		Code:    "23505",
		Message: marker,
		Detail:  "duplicate row contained " + marker,
	})
	if got := log.String(); !strings.Contains(got, `kind="read pairing"`) || !strings.Contains(got, `sqlstate=23505`) {
		t.Errorf("PostgreSQL log = %q, want kind and sqlstate", got)
	}
	if got := log.String(); strings.Contains(got, marker) {
		t.Errorf("PostgreSQL log contains error details: %q", got)
	}

	log.Reset()
	wrapped := fmt.Errorf("operation failed: %w", &pgconn.PgError{Code: "40001", Message: marker})
	WriteInternalError(httptest.NewRecorder(), logger, "submit code", wrapped)
	if got := log.String(); !strings.Contains(got, `kind="submit code"`) || !strings.Contains(got, `sqlstate=40001`) || strings.Contains(got, marker) {
		t.Errorf("wrapped PostgreSQL log = %q, want kind and sqlstate only", got)
	}

	log.Reset()
	WriteInternalError(httptest.NewRecorder(), logger, "submit code", fmt.Errorf("plain marker %s", marker))
	if got := log.String(); !strings.Contains(got, `kind="submit code"`) || strings.Contains(got, "sqlstate") || strings.Contains(got, marker) {
		t.Errorf("plain-error log = %q, want only operation kind", got)
	}

	log.Reset()
	WriteInternalError(httptest.NewRecorder(), logger, "start pairing", nil)
	if got := log.String(); !strings.Contains(got, `kind="start pairing"`) || strings.Contains(got, "sqlstate") || strings.Contains(got, "err=") {
		t.Errorf("nil-error log = %q, want only operation kind", got)
	}

	var nilLogger *slog.Logger
	if err := recoveredPanic(func() {
		WriteInternalError(httptest.NewRecorder(), nilLogger, "read pairing", errors.New(marker))
	}); err != nil {
		t.Fatalf("nil logger panicked: %v", err)
	}
}

func recoveredPanic(fn func()) (panicValue any) {
	defer func() { panicValue = recover() }()
	fn()
	return nil
}
