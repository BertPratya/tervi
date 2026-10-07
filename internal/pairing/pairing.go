// Package pairing contains the pairing routes and background work.
package pairing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/jackc/pgx/v5"
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
	mux.HandleFunc("GET /api/v1/approvals/current", deps.readApproval)
	mux.HandleFunc("POST /api/v1/approvals/current/accept", deps.accept)
	mux.HandleFunc("POST /api/v1/approvals/current/reject", deps.reject)
}

// StartCleanup starts pairing cleanup work.
func StartCleanup(_ context.Context, _ *pgxpool.Pool, _ *slog.Logger) {}

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
	if utf8.RuneCountInString(input.Hostname) > 64 || utf8.RuneCountInString(input.OSName) > 64 || utf8.RuneCountInString(input.OSVersion) > 32 {
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
	err := deps.Pool.QueryRow(r.Context(), `SELECT
		CASE WHEN status IN ('waiting_for_approval', 'waiting_for_code') AND expires_at <= now()
			THEN 'expired' ELSE status END,
		GREATEST(0, CEIL(EXTRACT(EPOCH FROM expires_at - now()))::integer),
		tries_left
		FROM pairing_requests WHERE polling_key_hash = $1`, secret.Hash(secret.FromString(proof))).
		Scan(&status, &expiresInSeconds, &triesLeft)
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
	writeJSON(w, http.StatusOK, body)
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
		CASE WHEN pr.status IN ('waiting_for_approval', 'waiting_for_code') AND pr.expires_at <= now()
			THEN 'expired' ELSE pr.status END,
		pr.hostname, pr.os_name, pr.os_version, coalesce(pr.pairing_code, ''),
		coalesce(pr.failure_reason, ''), coalesce(m.display_name, '')
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
