// Package pairing contains the pairing routes and background work.
package pairing

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxRequestBodyBytes = 4 * 1024

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

// StartCleanup starts pairing cleanup work.
func StartCleanup(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	startCleanup(ctx, pool, logger, time.Minute)
}

func startCleanup(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := cleanupPass(ctx, pool); err != nil && ctx.Err() == nil {
					logger.Error("pairing cleanup failed", "error", err)
				}
			}
		}
	}()
}

func cleanupPass(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE pairing_requests SET status = 'expired'
		WHERE status IN ('waiting_for_approval', 'waiting_for_code') AND expires_at <= now()`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `WITH expired_machines AS (
			UPDATE machines SET status = 'expired'
			WHERE status = 'pending' AND expires_at <= now()
			RETURNING pairing_request_id
		)
		UPDATE pairing_requests AS pr
		SET status = 'failed', failure_reason = 'not_confirmed'
		FROM expired_machines AS em
		WHERE pr.id = em.pairing_request_id AND pr.status = 'finishing'`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type startInput struct {
	Hostname  string
	OSName    string
	OSVersion string
}

type startResponse struct {
	PollingKey       string `json:"polling_key"`
	ApprovalURL      string `json:"approval_url"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

func (deps Deps) start(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeStartInput(w, r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if utf8.RuneCountInString(input.Hostname) > 64 || utf8.RuneCountInString(input.OSName) > 64 || utf8.RuneCountInString(input.OSVersion) > 32 ||
		strings.ContainsRune(input.Hostname, '\x00') || strings.ContainsRune(input.OSName, '\x00') || strings.ContainsRune(input.OSVersion, '\x00') {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}

	pollingKey, err := secret.NewKey()
	if err != nil {
		writeInternalError(w)
		return
	}
	approvalKey, err := secret.NewKey()
	if err != nil {
		writeInternalError(w)
		return
	}
	_, err = deps.Pool.Exec(r.Context(), `INSERT INTO pairing_requests
		(hostname, os_name, os_version, status, polling_key_hash, approval_key_hash, expires_at)
		VALUES ($1, $2, $3, 'waiting_for_approval', $4, $5, now() + interval '10 minutes')`,
		input.Hostname, input.OSName, input.OSVersion, secret.Hash(pollingKey), secret.Hash(approvalKey))
	if err != nil {
		writeInternalError(w)
		return
	}

	writeJSON(w, http.StatusCreated, startResponse{
		PollingKey:       pollingKey.Reveal(),
		ApprovalURL:      deps.PublicURL + "/pair/" + approvalKey.Reveal(),
		ExpiresInSeconds: 600,
	})
}

func decodeStartInput(w http.ResponseWriter, r *http.Request) (startInput, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodyBytes+1))
	if err != nil || len(body) > maxRequestBodyBytes {
		return startInput{}, false
	}
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return startInput{}, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return startInput{}, false
	}
	var input startInput
	for name, destination := range map[string]*string{
		"hostname":   &input.Hostname,
		"os_name":    &input.OSName,
		"os_version": &input.OSVersion,
	} {
		value, exists := fields[name]
		if !exists {
			continue
		}
		value = []byte(strings.TrimSpace(string(value)))
		if len(value) == 0 || value[0] != '"' || json.Unmarshal(value, destination) != nil {
			return startInput{}, false
		}
	}
	return input, true
}

func (deps Deps) poll(w http.ResponseWriter, r *http.Request) {
	proof, ok := bearerProof(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unknown_key")
		return
	}
	var status string
	var expiresInSeconds int
	var triesLeft int
	var failureReason string
	err := deps.Pool.QueryRow(r.Context(), `SELECT
		CASE
			WHEN pr.status IN ('waiting_for_approval', 'waiting_for_code') AND pr.expires_at <= now() THEN 'expired'
			WHEN pr.status = 'finishing' AND m.status = 'pending' AND m.expires_at <= now() THEN 'failed'
			ELSE pr.status
		END,
		GREATEST(0, CEIL(EXTRACT(EPOCH FROM pr.expires_at - now()))::integer),
		pr.tries_left,
		CASE WHEN pr.status = 'finishing' AND m.status = 'pending' AND m.expires_at <= now()
			THEN 'not_confirmed' ELSE coalesce(pr.failure_reason, '') END
		FROM pairing_requests AS pr
		LEFT JOIN machines AS m ON m.pairing_request_id = pr.id
		WHERE pr.polling_key_hash = $1`, secret.Hash(secret.FromString(proof))).
		Scan(&status, &expiresInSeconds, &triesLeft, &failureReason)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "unknown_key")
		return
	}
	if err != nil {
		writeInternalError(w)
		return
	}
	body := map[string]any{
		"status":             status,
		"expires_in_seconds": expiresInSeconds,
	}
	if status == "waiting_for_code" {
		body["tries_left"] = triesLeft
	}
	if status == "failed" {
		body["failure_reason"] = failureReason
	}
	writeJSON(w, http.StatusOK, body)
}

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
		CASE
			WHEN pr.status IN ('waiting_for_approval', 'waiting_for_code') AND pr.expires_at <= now() THEN 'expired'
			WHEN pr.status = 'finishing' AND m.status = 'pending' AND m.expires_at <= now() THEN 'failed'
			ELSE pr.status
		END,
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
	err := deps.Pool.QueryRow(r.Context(), `SELECT
		CASE
			WHEN pr.status IN ('waiting_for_approval', 'waiting_for_code') AND pr.expires_at <= now() THEN 'expired'
			WHEN pr.status = 'finishing' AND m.status = 'pending' AND m.expires_at <= now() THEN 'failed'
			ELSE pr.status
		END
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
	err := pool.QueryRow(ctx, `SELECT
		CASE
			WHEN pr.status IN ('waiting_for_approval', 'waiting_for_code') AND pr.expires_at <= now() THEN 'expired'
			WHEN pr.status = 'finishing' AND m.status = 'pending' AND m.expires_at <= now() THEN 'failed'
			ELSE pr.status
		END,
		pr.hostname, pr.os_name, pr.os_version, coalesce(pr.pairing_code, ''),
		CASE WHEN pr.status = 'finishing' AND m.status = 'pending' AND m.expires_at <= now()
			THEN 'not_confirmed' ELSE coalesce(pr.failure_reason, '') END,
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

func bearerProof(r *http.Request) (string, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func writeInternalError(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "internal_error")
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
