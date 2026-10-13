package pairing

import (
	"errors"
	"net/http"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/server/httpapi"
	"github.com/jackc/pgx/v5"
)

func (deps Deps) poll(w http.ResponseWriter, r *http.Request) {
	proof, ok := httpapi.BearerProof(r)
	if !ok {
		httpapi.WriteError(w, http.StatusUnauthorized, "unknown_key")
		return
	}
	var status string
	var expiresInSeconds int
	var triesLeft int
	var failureReason string
	err := deps.Pool.QueryRow(r.Context(), `SELECT `+effectiveStatus+`,
		GREATEST(0, CEIL(EXTRACT(EPOCH FROM pr.expires_at - now()))::integer),
		pr.tries_left,
		`+effectiveFailureReason+`
		FROM pairing_requests AS pr
		LEFT JOIN machines AS m ON m.pairing_request_id = pr.id
		WHERE pr.polling_key_hash = $1`, secret.Hash(secret.FromString(proof))).
		Scan(&status, &expiresInSeconds, &triesLeft, &failureReason)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, http.StatusUnauthorized, "unknown_key")
		return
	}
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, "read pairing", err)
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
	httpapi.WriteJSON(w, http.StatusOK, body)
}
