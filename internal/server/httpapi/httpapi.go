// Package httpapi contains the server's shared JSON API conventions.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// BearerProof extracts the proof from an Authorization header using the Bearer scheme.
func BearerProof(r *http.Request) (string, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

// WriteInternalError sends the standard internal error response and logs only
// the fixed operation kind and, for PostgreSQL errors, its SQLSTATE.
func WriteInternalError(w http.ResponseWriter, logger *slog.Logger, kind string, err error) {
	if logger != nil {
		attrs := []any{"kind", kind}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr != nil {
			attrs = append(attrs, "sqlstate", pgErr.Code)
		}
		logger.Error("internal error", attrs...)
	}
	WriteError(w, http.StatusInternalServerError, "internal_error")
}

// WriteError sends a JSON error response.
func WriteError(w http.ResponseWriter, status int, code string) {
	WriteJSON(w, status, map[string]string{"error": code})
}

// WriteJSON sends a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
