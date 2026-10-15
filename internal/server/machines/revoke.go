package machines

import (
	"net/http"

	"github.com/bertpratya/tervi/internal/server/httpapi"
	"github.com/jackc/pgx/v5/pgtype"
)

type revokeHandler struct {
	deps Deps
}

func (handler revokeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var machineID pgtype.UUID
	if err := machineID.Scan(r.PathValue("machine_id")); err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "unknown_machine")
		return
	}

	var exists, active bool
	err := handler.deps.Pool.QueryRow(r.Context(), `SELECT
		EXISTS (SELECT 1 FROM machines WHERE machine_id = $1),
		EXISTS (SELECT 1 FROM credentials WHERE machine_id = $1 AND status = 'active')`, machineID).
		Scan(&exists, &active)
	if err != nil {
		httpapi.WriteInternalError(w, handler.deps.Logger, "revoke machine", err)
		return
	}
	if !exists {
		httpapi.WriteError(w, http.StatusNotFound, "unknown_machine")
		return
	}
	if !active {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := revokeMachineCredential(r.Context(), handler.deps, machineID.String()); err != nil {
		httpapi.WriteInternalError(w, handler.deps.Logger, "revoke machine", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
