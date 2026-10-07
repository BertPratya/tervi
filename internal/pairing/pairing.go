// Package pairing contains the pairing routes and background work.
package pairing

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Deps contains the dependencies used by pairing handlers.
type Deps struct {
	Pool      *pgxpool.Pool
	PublicURL string
	Logger    *slog.Logger
}

// Register registers pairing routes.
func Register(_ *http.ServeMux, _ Deps) {}

// StartCleanup starts pairing cleanup work.
func StartCleanup(_ context.Context, _ *pgxpool.Pool, _ *slog.Logger) {}
