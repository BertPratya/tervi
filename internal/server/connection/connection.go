// Package connection serves machine WebSocket connections and tracks which
// machines are currently online.
package connection

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Deps holds the dependencies used by the machine connection route.
type Deps struct {
	Pool              *pgxpool.Pool
	Logger            *slog.Logger
	List              *List
	CheckInterval     time.Duration
	ConnectionTimeout time.Duration
	Now               func() time.Time
}

func (deps Deps) normalized() Deps {
	if deps.List == nil {
		deps.List = NewList()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.CheckInterval == 0 && deps.ConnectionTimeout == 0 {
		deps.CheckInterval = 10 * time.Second
		deps.ConnectionTimeout = 30 * time.Second
	}
	return deps
}

// Register registers the machine connection route.
func Register(mux *http.ServeMux, deps Deps) {
	deps = deps.normalized()
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	deps.List.setLogger(deps.Logger)
	mux.Handle("GET /api/v1/machines/current/connection", connectionHandler{deps: deps})
}
