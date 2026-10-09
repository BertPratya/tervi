package pairing

import (
	"errors"
	"net/http"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (deps Deps) acknowledge(w http.ResponseWriter, r *http.Request) {
	deps.changeMachine(w, r, true)
}
func (deps Deps) saveFailure(w http.ResponseWriter, r *http.Request) {
	deps.changeMachine(w, r, false)
}
func (deps Deps) changeMachine(w http.ResponseWriter, r *http.Request, acknowledgment bool) {
	proof, ok := bearerProof(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unknown_credential")
		return
	}
	credentialHash := secret.Hash(secret.FromString(proof))
	tx, err := deps.Pool.Begin(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var machineID, requestID string
	var updateErr error
	if acknowledgment {
		updateErr = tx.QueryRow(r.Context(), `UPDATE machines SET status = 'active'
			WHERE credential_hash = $1 AND status = 'pending' AND expires_at > now()
			RETURNING machine_id::text, pairing_request_id::text`, credentialHash).Scan(&machineID, &requestID)
	} else {
		updateErr = tx.QueryRow(r.Context(), `UPDATE machines SET status = 'failed'
			WHERE credential_hash = $1 AND status = 'pending' AND expires_at > now()
			RETURNING machine_id::text, pairing_request_id::text`, credentialHash).Scan(&machineID, &requestID)
	}
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
			writeInternalError(w)
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			writeInternalError(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"result": "ok"})
		return
	}
	if !errors.Is(updateErr, pgx.ErrNoRows) {
		writeInternalError(w)
		return
	}
	_ = tx.Rollback(r.Context())
	var status string
	err = deps.Pool.QueryRow(r.Context(), `SELECT CASE WHEN status = 'pending' AND expires_at <= now()
		THEN 'expired' ELSE status END FROM machines WHERE credential_hash = $1`, credentialHash).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "unknown_credential")
		return
	}
	if err != nil {
		writeInternalError(w)
		return
	}
	result := ""
	switch status {
	case "active":
		if acknowledgment {
			result = "ok"
		} else {
			result = "active"
		}
	case "failed", "expired":
		result = status
	default:
		writeInternalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": result})
}
