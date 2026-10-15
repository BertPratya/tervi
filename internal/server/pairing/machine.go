package pairing

import (
	"errors"
	"net/http"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/server/credential"
	"github.com/bertpratya/tervi/internal/server/httpapi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const changeCredentialQuery = `UPDATE credentials SET status = $2
	WHERE credential_id = $1 AND status = 'pending' AND expires_at > now()
	RETURNING machine_id::text, pairing_request_id::text`

func (deps Deps) acknowledge(w http.ResponseWriter, r *http.Request) {
	deps.changeMachine(w, r, true)
}

func (deps Deps) saveFailure(w http.ResponseWriter, r *http.Request) {
	deps.changeMachine(w, r, false)
}

func (deps Deps) changeMachine(w http.ResponseWriter, r *http.Request, acknowledgment bool) {
	kind := "save machine failure"
	if acknowledgment {
		kind = "acknowledge machine"
	}
	proof, ok := httpapi.BearerProof(r)
	if !ok {
		httpapi.WriteError(w, http.StatusUnauthorized, "unknown_credential")
		return
	}
	credentialRow, found, err := credential.Find(r.Context(), deps.Pool, secret.FromString(proof))
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, kind, err)
		return
	}
	if !found {
		httpapi.WriteError(w, http.StatusUnauthorized, "unknown_credential")
		return
	}
	if credentialRow.Status == credential.StatusRevoked {
		httpapi.WriteError(w, http.StatusUnauthorized, "credential_revoked")
		return
	}
	tx, err := deps.Pool.Begin(r.Context())
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, kind, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var machineID, requestID string
	nextStatus := credential.StatusFailed
	if acknowledgment {
		nextStatus = credential.StatusActive
	}
	updateErr := tx.QueryRow(r.Context(), changeCredentialQuery, credentialRow.CredentialID, nextStatus).Scan(&machineID, &requestID)
	if updateErr == nil {
		var tag pgconn.CommandTag
		if acknowledgment {
			tag, err = tx.Exec(r.Context(), `UPDATE pairing_requests SET status = 'paired'
				WHERE id = $1 AND status = 'finishing'`, requestID)
		} else {
			tag, err = tx.Exec(r.Context(), `UPDATE pairing_requests SET status = 'failed', failure_reason = 'not_saved'
				WHERE id = $1 AND status = 'finishing'`, requestID)
		}
		if err != nil || tag.RowsAffected() != 1 {
			httpapi.WriteInternalError(w, deps.Logger, kind, err)
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			httpapi.WriteInternalError(w, deps.Logger, kind, err)
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, map[string]string{"result": "ok"})
		return
	}
	if !errors.Is(updateErr, pgx.ErrNoRows) {
		httpapi.WriteInternalError(w, deps.Logger, kind, updateErr)
		return
	}
	_ = tx.Rollback(r.Context())
	credentialRow, found, err = credential.Find(r.Context(), deps.Pool, secret.FromString(proof))
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, kind, err)
		return
	}
	if !found {
		httpapi.WriteError(w, http.StatusUnauthorized, "unknown_credential")
		return
	}
	result := ""
	switch credentialRow.Status {
	case credential.StatusRevoked:
		httpapi.WriteError(w, http.StatusUnauthorized, "credential_revoked")
		return
	case credential.StatusActive:
		if acknowledgment {
			result = "ok"
		} else {
			result = "active"
		}
	case credential.StatusFailed, credential.StatusExpired:
		result = credentialRow.Status
	default:
		httpapi.WriteInternalError(w, deps.Logger, kind, nil)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"result": result})
}
