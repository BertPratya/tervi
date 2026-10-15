// Package machines provides the paired machines list API.
package machines

import (
	"log/slog"
	"net/http"

	"github.com/bertpratya/tervi/internal/server/connection"
	"github.com/bertpratya/tervi/internal/server/httpapi"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deps contains the services used by the machines list.
type Deps struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	List   *connection.List
}

type listedMachine struct {
	MachineID          string `json:"machine_id"`
	DisplayName        string `json:"display_name"`
	OSName             string `json:"os_name"`
	OSVersion          string `json:"os_version"`
	Online             bool   `json:"online"`
	LastSeenSecondsAgo *int64 `json:"last_seen_seconds_ago"`
}

// Register adds the machines list API route to mux.
func Register(mux *http.ServeMux, deps Deps) {
	mux.Handle("GET /api/v1/machines", listHandler{deps: deps})
}

type listHandler struct {
	deps Deps
}

func (handler listHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rows, err := handler.deps.Pool.Query(r.Context(), `SELECT machine_id::text, display_name, os_name, os_version,
		CASE WHEN last_seen_at IS NULL THEN NULL
			ELSE GREATEST(FLOOR(EXTRACT(EPOCH FROM (now() - last_seen_at))), 0)::bigint
		END AS last_seen_seconds_ago
		FROM machines AS m
		WHERE EXISTS (SELECT 1 FROM credentials AS c WHERE c.machine_id = m.machine_id AND c.status = 'active')
		ORDER BY display_name, machine_id`)
	if err != nil {
		httpapi.WriteInternalError(w, handler.deps.Logger, "list machines", err)
		return
	}
	defer rows.Close()

	onlineIDs := map[string]bool{}
	if handler.deps.List != nil {
		onlineIDs = handler.deps.List.OnlineIDs()
	}
	machines := make([]listedMachine, 0)
	for rows.Next() {
		var machine listedMachine
		var seconds pgtype.Int8
		if err := rows.Scan(&machine.MachineID, &machine.DisplayName, &machine.OSName, &machine.OSVersion, &seconds); err != nil {
			httpapi.WriteInternalError(w, handler.deps.Logger, "list machines", err)
			return
		}
		machine.Online = onlineIDs[machine.MachineID]
		if seconds.Valid {
			value := seconds.Int64
			machine.LastSeenSecondsAgo = &value
		}
		machines = append(machines, machine)
	}
	if err := rows.Err(); err != nil {
		httpapi.WriteInternalError(w, handler.deps.Logger, "list machines", err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"machines": machines})
}
