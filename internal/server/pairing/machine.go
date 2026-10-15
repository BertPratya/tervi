package pairing

import (
	"errors"
	"net/http"

	"github.com/bertpratya/tervi/internal/protocol"
	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/server/credential"
	"github.com/bertpratya/tervi/internal/server/httpapi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const lockMatchedMachineQuery = `SELECT m.machine_id::text
	FROM credentials AS current
	JOIN pairing_requests AS pr ON pr.id = current.pairing_request_id
	JOIN machines AS m ON m.machine_id = pr.matched_machine_id
	WHERE current.credential_hash = $1
	FOR UPDATE OF m`

const reservePendingCredentialQuery = `UPDATE credentials SET status = 'pending'
	WHERE credential_hash = $1 AND status = 'pending' AND expires_at > now()
	RETURNING credential_id::text, machine_id::text, pairing_request_id::text`

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
		httpapi.WriteError(w, http.StatusUnauthorized, protocol.ErrorUnknownCredential)
		return
	}
	tx, err := deps.Pool.Begin(r.Context())
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, kind, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	matchedMachineID := ""
	if acknowledgment {
		err = tx.QueryRow(r.Context(), lockMatchedMachineQuery, secret.Hash(secret.FromString(proof))).Scan(&matchedMachineID)
		if errors.Is(err, pgx.ErrNoRows) {
			matchedMachineID = ""
		} else if err != nil {
			httpapi.WriteInternalError(w, deps.Logger, kind, err)
			return
		}
	}

	credentialRow, found, err := credential.Find(r.Context(), tx, secret.FromString(proof))
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, kind, err)
		return
	}
	if !found {
		_ = tx.Rollback(r.Context())
		httpapi.WriteError(w, http.StatusUnauthorized, protocol.ErrorUnknownCredential)
		return
	}
	if credentialRow.Status == credential.StatusRevoked {
		_ = tx.Rollback(r.Context())
		httpapi.WriteError(w, http.StatusUnauthorized, protocol.ErrorCredentialRevoked)
		return
	}
	if credentialRow.Status != credential.StatusPending {
		_ = tx.Rollback(r.Context())
		deps.writeExistingCredentialResult(w, r, proof, acknowledgment, kind)
		return
	}

	var credentialID, machineID, requestID string
	err = tx.QueryRow(r.Context(), reservePendingCredentialQuery, secret.Hash(secret.FromString(proof))).
		Scan(&credentialID, &machineID, &requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(r.Context())
		deps.writeExistingCredentialResult(w, r, proof, acknowledgment, kind)
		return
	}
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, kind, err)
		return
	}
	if matchedMachineID != "" && machineID != matchedMachineID {
		httpapi.WriteInternalError(w, deps.Logger, kind, errors.New("matched machine differs from credential machine"))
		return
	}

	nextStatus := credential.StatusFailed
	if acknowledgment {
		nextStatus = credential.StatusActive
	}
	var revoked bool
	if acknowledgment && matchedMachineID != "" {
		var hostname, osName, osVersion string
		if err := tx.QueryRow(r.Context(), `SELECT hostname, os_name, os_version FROM pairing_requests WHERE id = $1`, requestID).
			Scan(&hostname, &osName, &osVersion); err != nil {
			httpapi.WriteInternalError(w, deps.Logger, kind, err)
			return
		}
		revoked, err = credential.Revoke(r.Context(), tx, machineID)
		if err != nil {
			httpapi.WriteInternalError(w, deps.Logger, kind, err)
			return
		}
		if _, err := tx.Exec(r.Context(), `UPDATE machines SET hostname = $2, os_name = $3, os_version = $4, display_name = $5
			WHERE machine_id = $1`, machineID, hostname, osName, osVersion, displayNameFor(hostname)); err != nil {
			httpapi.WriteInternalError(w, deps.Logger, kind, err)
			return
		}
	}

	var changedMachineID, changedRequestID string
	updateErr := tx.QueryRow(r.Context(), changeCredentialQuery, credentialID, nextStatus).
		Scan(&changedMachineID, &changedRequestID)
	if errors.Is(updateErr, pgx.ErrNoRows) {
		_ = tx.Rollback(r.Context())
		deps.writeExistingCredentialResult(w, r, proof, acknowledgment, kind)
		return
	}
	if updateErr != nil {
		httpapi.WriteInternalError(w, deps.Logger, kind, updateErr)
		return
	}
	if changedMachineID != machineID || changedRequestID != requestID {
		httpapi.WriteInternalError(w, deps.Logger, kind, errors.New("credential changed during machine update"))
		return
	}

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
	if revoked && deps.List != nil {
		deps.List.CloseRevoked(machineID)
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"result": "ok"})
}

func (deps Deps) writeExistingCredentialResult(w http.ResponseWriter, r *http.Request, proof string, acknowledgment bool, kind string) {
	credentialRow, found, err := credential.Find(r.Context(), deps.Pool, secret.FromString(proof))
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, kind, err)
		return
	}
	if !found {
		httpapi.WriteError(w, http.StatusUnauthorized, protocol.ErrorUnknownCredential)
		return
	}
	switch credentialRow.Status {
	case credential.StatusRevoked:
		httpapi.WriteError(w, http.StatusUnauthorized, protocol.ErrorCredentialRevoked)
	case credential.StatusActive:
		result := "active"
		if acknowledgment {
			result = "ok"
		}
		httpapi.WriteJSON(w, http.StatusOK, map[string]string{"result": result})
	case credential.StatusFailed, credential.StatusExpired:
		httpapi.WriteJSON(w, http.StatusOK, map[string]string{"result": credentialRow.Status})
	default:
		httpapi.WriteInternalError(w, deps.Logger, kind, nil)
	}
}
