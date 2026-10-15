package pairing

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/server/httpapi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	hasEarlier    bool
	earlierName   string
	stillPaired   bool
	unpairedAt    pgtype.Timestamptz
}

type earlierMachineAnswer struct {
	DisplayName string     `json:"display_name"`
	StillPaired bool       `json:"still_paired"`
	UnpairedAt  *time.Time `json:"unpaired_at"`
}

func (deps Deps) readApproval(w http.ResponseWriter, r *http.Request) {
	proof, ok := httpapi.BearerProof(r)
	if !ok {
		httpapi.WriteError(w, http.StatusNotFound, "invalid_link")
		return
	}
	row, err := queryApproval(r.Context(), deps.Pool, secret.Hash(secret.FromString(proof)))
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, http.StatusNotFound, "invalid_link")
		return
	}
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, "read approval", err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, approvalBody(row, false))
}

func (deps Deps) accept(w http.ResponseWriter, r *http.Request) {
	deps.decide(w, r, true)
}

func (deps Deps) reject(w http.ResponseWriter, r *http.Request) {
	deps.decide(w, r, false)
}

func (deps Deps) decide(w http.ResponseWriter, r *http.Request, accept bool) {
	kind := "reject approval"
	if accept {
		kind = "accept approval"
	}
	proof, ok := httpapi.BearerProof(r)
	if !ok {
		httpapi.WriteError(w, http.StatusNotFound, "invalid_link")
		return
	}
	approvalHash := secret.Hash(secret.FromString(proof))
	var row approvalRow
	var updated int
	var err error
	if accept {
		code, generationErr := secret.NewPairingCode()
		if generationErr != nil {
			httpapi.WriteInternalError(w, deps.Logger, kind, generationErr)
			return
		}
		err = deps.Pool.QueryRow(r.Context(), `UPDATE pairing_requests
			SET status = 'waiting_for_code', pairing_code = $2
			WHERE approval_key_hash = $1 AND status = 'waiting_for_approval' AND expires_at > now()
			RETURNING 1`, approvalHash, code.Reveal()).Scan(&updated)
	} else {
		err = deps.Pool.QueryRow(r.Context(), `UPDATE pairing_requests
			SET status = 'rejected'
			WHERE approval_key_hash = $1 AND status = 'waiting_for_approval' AND expires_at > now()
			RETURNING 1`, approvalHash).Scan(&updated)
	}
	if err == nil {
		// Return the current approval, including any matched machine, after the decision.
		row, err = queryApproval(r.Context(), deps.Pool, approvalHash)
		if err != nil {
			httpapi.WriteInternalError(w, deps.Logger, kind, err)
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, approvalBody(row, false))
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteInternalError(w, deps.Logger, kind, err)
		return
	}

	row, err = queryApproval(r.Context(), deps.Pool, approvalHash)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, http.StatusNotFound, "invalid_link")
		return
	}
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, kind, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, approvalBody(row, row.status != "expired"))
}

func queryApproval(ctx context.Context, pool *pgxpool.Pool, approvalHash []byte) (approvalRow, error) {
	var row approvalRow
	err := pool.QueryRow(ctx, `SELECT `+effectiveStatus+`,
		pr.hostname, pr.os_name, pr.os_version, coalesce(pr.pairing_code, ''),
		`+effectiveFailureReason+`,
		coalesce(m.display_name, ''),
		earlier.machine_id IS NOT NULL,
		coalesce(earlier.display_name, ''),
		EXISTS (SELECT 1 FROM credentials AS active
			WHERE active.machine_id = earlier.machine_id AND active.status = 'active'),
		(SELECT ended.ended_at FROM credentials AS ended
			WHERE ended.machine_id = earlier.machine_id AND ended.ended_at IS NOT NULL
			ORDER BY ended.ended_at DESC, ended.credential_id DESC LIMIT 1)
		FROM pairing_requests AS pr `+pairingCredentialJoin+` `+pairingMachineJoin+`
		LEFT JOIN machines AS earlier ON earlier.machine_id = pr.matched_machine_id
		WHERE pr.approval_key_hash = $1`, approvalHash).Scan(
		&row.status, &row.hostname, &row.osName, &row.osVersion, &row.pairingCode, &row.failureReason, &row.displayName,
		&row.hasEarlier, &row.earlierName, &row.stillPaired, &row.unpairedAt)
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
	if row.hasEarlier {
		var unpairedAt *time.Time
		if !row.stillPaired && row.unpairedAt.Valid {
			value := row.unpairedAt.Time.UTC()
			unpairedAt = &value
		}
		body["earlier_machine"] = earlierMachineAnswer{
			DisplayName: row.earlierName,
			StillPaired: row.stillPaired,
			UnpairedAt:  unpairedAt,
		}
	}
	return body
}
