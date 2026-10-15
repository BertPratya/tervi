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

	var exists bool
	err := handler.deps.Pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM machines WHERE machine_id = $1)`, machineID).
		Scan(&exists)
	if err != nil {
		httpapi.WriteInternalError(w, handler.deps.Logger, "revoke machine", err)
		return
	}
	if !exists {
		httpapi.WriteError(w, http.StatusNotFound, "unknown_machine")
		return
	}
	if err := revokeMachineCredential(r.Context(), handler.deps, machineID.String()); err != nil {
		httpapi.WriteInternalError(w, handler.deps.Logger, "revoke machine", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
