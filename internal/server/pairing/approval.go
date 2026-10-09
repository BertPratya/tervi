package pairing

import (
	"context"
	"errors"
	"net/http"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type approvalRow struct {
	status        string
	hostname      string
	osName        string
	osVersion     string
	pairingCode   string
	failureReason string
	displayName   string
}

func (deps Deps) readApproval(w http.ResponseWriter, r *http.Request) {
	proof, ok := bearerProof(r)
	if !ok {
		writeError(w, http.StatusNotFound, "invalid_link")
		return
	}
	row, err := queryApproval(r.Context(), deps.Pool, secret.Hash(secret.FromString(proof)))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "invalid_link")
		return
	}
	if err != nil {
		writeInternalError(w)
		return
	}
	writeJSON(w, http.StatusOK, approvalBody(row, false))
}

func (deps Deps) accept(w http.ResponseWriter, r *http.Request) {
	deps.decide(w, r, true)
}

func (deps Deps) reject(w http.ResponseWriter, r *http.Request) {
	deps.decide(w, r, false)
}

func (deps Deps) decide(w http.ResponseWriter, r *http.Request, accept bool) {
	proof, ok := bearerProof(r)
	if !ok {
		writeError(w, http.StatusNotFound, "invalid_link")
		return
	}
	approvalHash := secret.Hash(secret.FromString(proof))
	var row approvalRow
	var err error
	if accept {
		code, generationErr := secret.NewPairingCode()
		if generationErr != nil {
			writeInternalError(w)
			return
		}
		err = deps.Pool.QueryRow(r.Context(), `UPDATE pairing_requests
			SET status = 'waiting_for_code', pairing_code = $2
			WHERE approval_key_hash = $1 AND status = 'waiting_for_approval' AND expires_at > now()
			RETURNING status, hostname, os_name, os_version, pairing_code,
				coalesce(failure_reason, ''), ''`, approvalHash, code.Reveal()).Scan(
			&row.status, &row.hostname, &row.osName, &row.osVersion, &row.pairingCode, &row.failureReason, &row.displayName)
	} else {
		err = deps.Pool.QueryRow(r.Context(), `UPDATE pairing_requests
			SET status = 'rejected'
			WHERE approval_key_hash = $1 AND status = 'waiting_for_approval' AND expires_at > now()
			RETURNING status, hostname, os_name, os_version, coalesce(pairing_code, ''),
				coalesce(failure_reason, ''), ''`, approvalHash).Scan(
			&row.status, &row.hostname, &row.osName, &row.osVersion, &row.pairingCode, &row.failureReason, &row.displayName)
	}
	if err == nil {
		writeJSON(w, http.StatusOK, approvalBody(row, false))
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeInternalError(w)
		return
	}

	row, err = queryApproval(r.Context(), deps.Pool, approvalHash)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "invalid_link")
		return
	}
	if err != nil {
		writeInternalError(w)
		return
	}
	writeJSON(w, http.StatusOK, approvalBody(row, row.status != "expired"))
}

func queryApproval(ctx context.Context, pool *pgxpool.Pool, approvalHash []byte) (approvalRow, error) {
	var row approvalRow
	err := pool.QueryRow(ctx, `SELECT `+effectiveStatus+`,
		pr.hostname, pr.os_name, pr.os_version, coalesce(pr.pairing_code, ''),
		`+effectiveFailureReason+`,
		coalesce(m.display_name, '')
		FROM pairing_requests AS pr
		LEFT JOIN machines AS m ON m.pairing_request_id = pr.id
		WHERE pr.approval_key_hash = $1`, approvalHash).Scan(
		&row.status, &row.hostname, &row.osName, &row.osVersion, &row.pairingCode, &row.failureReason, &row.displayName)
	return row, err
}

func approvalBody(row approvalRow, alreadyDecided bool) map[string]any {
	body := map[string]any{
		"status":          row.status,
		"hostname":        row.hostname,
		"os_name":         row.osName,
		"os_version":      row.osVersion,
		"already_decided": alreadyDecided,
	}
	if row.status == "waiting_for_code" {
		body["pairing_code"] = row.pairingCode
	}
	if row.status == "failed" {
		body["failure_reason"] = row.failureReason
	}
	if row.status == "paired" {
		body["display_name"] = row.displayName
	}
	return body
}
