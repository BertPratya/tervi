package pairing

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/jackc/pgx/v5"
)

type codeInput struct {
	Code string `json:"code"`
}

func (deps Deps) submitCode(w http.ResponseWriter, r *http.Request) {
	proof, ok := bearerProof(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unknown_key")
		return
	}
	input, ok := decodeCodeInput(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	pollingHash := secret.Hash(secret.FromString(proof))
	var requestID, status, storedCode string
	var triesLeft int
	err := deps.Pool.QueryRow(r.Context(), `SELECT pr.id::text,
		`+effectiveStatus+`,
		coalesce(pr.pairing_code, ''),
		pr.tries_left
		FROM pairing_requests AS pr
		LEFT JOIN machines AS m ON m.pairing_request_id = pr.id
		WHERE pr.polling_key_hash = $1`, pollingHash).
		Scan(&requestID, &status, &storedCode, &triesLeft)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "unknown_key")
		return
	}
	if err != nil {
		writeInternalError(w)
		return
	}
	if status == "expired" {
		writeJSON(w, http.StatusOK, map[string]string{"result": "expired"})
		return
	}
	if status != "waiting_for_code" {
		writeJSON(w, http.StatusOK, map[string]string{"result": "not_waiting_for_code", "status": status})
		return
	}

	submitted := secret.NormalizeCode(input.Code)
	stored := secret.NormalizeCode(storedCode)
	if subtle.ConstantTimeCompare([]byte(submitted), []byte(stored)) == 1 {
		deps.issueCredential(w, r, requestID)
		return
	}

	err = deps.Pool.QueryRow(r.Context(), `UPDATE pairing_requests
		SET tries_left = tries_left - 1,
			status = CASE WHEN tries_left - 1 = 0 THEN 'failed' ELSE status END,
			failure_reason = CASE WHEN tries_left - 1 = 0 THEN 'wrong_codes' ELSE failure_reason END
		WHERE id = $1 AND status = 'waiting_for_code' AND tries_left > 0 AND expires_at > now()
		RETURNING tries_left, status`, requestID).Scan(&triesLeft, &status)
	if err == nil {
		if status == "failed" {
			writeJSON(w, http.StatusOK, map[string]string{"result": "failed"})
		} else {
			writeJSON(w, http.StatusOK, map[string]any{"result": "wrong_code", "tries_left": triesLeft})
		}
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeInternalError(w)
		return
	}
	deps.writeCodeUnavailable(w, r, requestID)
}
func decodeCodeInput(r *http.Request) (codeInput, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodyBytes+1))
	if err != nil || len(body) > maxRequestBodyBytes {
		return codeInput{}, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return codeInput{}, false
	}
	raw, exists := fields["code"]
	if !exists {
		return codeInput{}, false
	}
	var input codeInput
	if err := json.Unmarshal(raw, &input.Code); err != nil {
		return codeInput{}, false
	}
	return input, true
}
func (deps Deps) issueCredential(w http.ResponseWriter, r *http.Request, requestID string) {
	tx, err := deps.Pool.Begin(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var hostname, osName, osVersion string
	err = tx.QueryRow(r.Context(), `UPDATE pairing_requests SET status = 'finishing'
		WHERE id = $1 AND status = 'waiting_for_code' AND expires_at > now()
		RETURNING hostname, os_name, os_version`, requestID).
		Scan(&hostname, &osName, &osVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(r.Context())
		deps.writeCodeUnavailable(w, r, requestID)
		return
	}
	if err != nil {
		writeInternalError(w)
		return
	}
	credential, err := secret.NewKey()
	if err != nil {
		writeInternalError(w)
		return
	}
	displayName := hostname
	if displayName == "" {
		displayName = "unknown"
	}
	var machineID string
	err = tx.QueryRow(r.Context(), `INSERT INTO machines
		(pairing_request_id, hostname, os_name, os_version, display_name, credential_hash, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', now() + interval '5 minutes')
		RETURNING machine_id::text`, requestID, hostname, osName, osVersion, displayName, secret.Hash(credential)).Scan(&machineID)
	if err != nil {
		writeInternalError(w)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeInternalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"result":     "accepted",
		"credential": credential.Reveal(),
		"machine_id": machineID,
	})
}
func (deps Deps) writeCodeUnavailable(w http.ResponseWriter, r *http.Request, requestID string) {
	var status string
	err := deps.Pool.QueryRow(r.Context(), `SELECT `+effectiveStatus+`
		FROM pairing_requests AS pr
		LEFT JOIN machines AS m ON m.pairing_request_id = pr.id
		WHERE pr.id = $1`, requestID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "unknown_key")
		return
	}
	if err != nil {
		writeInternalError(w)
		return
	}
	if status == "expired" {
		writeJSON(w, http.StatusOK, map[string]string{"result": "expired"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "not_waiting_for_code", "status": status})
}
