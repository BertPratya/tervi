// Package pairing contains the pairing routes and background work.
package pairing

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

const maxRequestBodyBytes = 4 * 1024

const pairingCredentialJoin = `LEFT JOIN credentials AS c ON c.pairing_request_id = pr.id`
const pairingMachineJoin = `LEFT JOIN machines AS m ON m.machine_id = c.machine_id`

// Deps contains the dependencies used by pairing handlers.
type Deps struct {
	Pool      *pgxpool.Pool
	PublicURL string
	Logger    *slog.Logger
}

// Register registers pairing routes.
func Register(mux *http.ServeMux, deps Deps) {
	mux.HandleFunc("POST /api/v1/pairings", deps.start)
	mux.HandleFunc("GET /api/v1/pairings/current", deps.poll)
	mux.HandleFunc("POST /api/v1/pairings/current/code", deps.submitCode)
	mux.HandleFunc("POST /api/v1/machines/current/acknowledgment", deps.acknowledge)
	mux.HandleFunc("POST /api/v1/machines/current/save-failure", deps.saveFailure)
	mux.HandleFunc("GET /api/v1/approvals/current", deps.readApproval)
	mux.HandleFunc("POST /api/v1/approvals/current/accept", deps.accept)
	mux.HandleFunc("POST /api/v1/approvals/current/reject", deps.reject)
}
