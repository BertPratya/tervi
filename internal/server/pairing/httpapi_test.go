package pairing

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bertpratya/tervi/internal/server/db/dbtest"
)

func TestPairingInternalErrorLogged(t *testing.T) {
	pool := dbtest.New(t)
	pool.Close()
	var log strings.Builder
	mux := http.NewServeMux()
	Register(mux, Deps{
		Pool:      pool,
		PublicURL: testPublicURL,
		Logger:    slog.New(slog.NewTextHandler(&log, nil)),
	})
	const proof = "polling-proof-marker-do-not-log"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pairings/current", nil)
	req.Header.Set("Authorization", "Bearer "+proof)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError || strings.TrimSpace(rec.Body.String()) != `{"error":"internal_error"}` {
		t.Fatalf("response = (%d, %q), want 500 internal_error", rec.Code, rec.Body.String())
	}
	if got := log.String(); !strings.Contains(got, `kind="read pairing"`) {
		t.Errorf("log = %q, want read pairing kind", got)
	}
	if got := log.String(); strings.Contains(got, proof) {
		t.Errorf("log contains request proof: %q", got)
	}
}
