package machines

import (
	"net/http"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/server/credential"
	"github.com/bertpratya/tervi/internal/server/httpapi"
)

type unpairHandler struct {
	deps Deps
}

func (handler unpairHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	proof, ok := httpapi.BearerProof(r)
	if !ok {
		httpapi.WriteError(w, http.StatusUnauthorized, "unknown_credential")
		return
	}

	row, found, err := credential.Find(r.Context(), handler.deps.Pool, secret.FromString(proof))
	if err != nil {
		httpapi.WriteInternalError(w, handler.deps.Logger, "unpair machine", err)
		return
	}
	if !found || (row.Status != credential.StatusActive && row.Status != credential.StatusRevoked) {
		httpapi.WriteError(w, http.StatusUnauthorized, "unknown_credential")
		return
	}
	if row.Status == credential.StatusRevoked {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := revokeMachineCredential(r.Context(), handler.deps, row.MachineID); err != nil {
		httpapi.WriteInternalError(w, handler.deps.Logger, "unpair machine", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
