package pairing

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/server/httpapi"
)

type startInput struct {
	Hostname     string
	OSName       string
	OSVersion    string
	IdentityHash []byte
}

type startResponse struct {
	PollingKey       string `json:"polling_key"`
	ApprovalURL      string `json:"approval_url"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

func (deps Deps) start(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeStartInput(w, r)
	if !ok {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if utf8.RuneCountInString(input.Hostname) > 64 || utf8.RuneCountInString(input.OSName) > 64 || utf8.RuneCountInString(input.OSVersion) > 32 ||
		strings.ContainsRune(input.Hostname, '\x00') || strings.ContainsRune(input.OSName, '\x00') || strings.ContainsRune(input.OSVersion, '\x00') {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_input")
		return
	}

	pollingKey, err := secret.NewKey()
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, "start pairing", err)
		return
	}
	approvalKey, err := secret.NewKey()
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, "start pairing", err)
		return
	}
	tx, err := deps.Pool.Begin(r.Context())
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, "start pairing", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var requestID string
	err = tx.QueryRow(r.Context(), `INSERT INTO pairing_requests
		(hostname, os_name, os_version, identity_hash, status, polling_key_hash, approval_key_hash, expires_at)
		VALUES ($1, $2, $3, $4, 'waiting_for_approval', $5, $6, now() + interval '10 minutes')
		RETURNING id::text`, input.Hostname, input.OSName, input.OSVersion, input.IdentityHash,
		secret.Hash(pollingKey), secret.Hash(approvalKey)).Scan(&requestID)
	if err != nil {
		httpapi.WriteInternalError(w, deps.Logger, "start pairing", err)
		return
	}
	if len(input.IdentityHash) > 0 {
		machineID, err := findMatchingMachine(r.Context(), tx, input.IdentityHash)
		if err != nil {
			httpapi.WriteInternalError(w, deps.Logger, "start pairing", err)
			return
		}
		if machineID != "" {
			if _, err := tx.Exec(r.Context(), `UPDATE pairing_requests SET matched_machine_id = $2 WHERE id = $1`, requestID, machineID); err != nil {
				httpapi.WriteInternalError(w, deps.Logger, "start pairing", err)
				return
			}
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		httpapi.WriteInternalError(w, deps.Logger, "start pairing", err)
		return
	}

	httpapi.WriteJSON(w, http.StatusCreated, startResponse{
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
	if value, exists := fields["identity_hash"]; exists {
		value = []byte(strings.TrimSpace(string(value)))
		if len(value) == 0 || value[0] != '"' {
			return startInput{}, false
		}
		var identity string
		if err := json.Unmarshal(value, &identity); err != nil || len(identity) != 64 {
			return startInput{}, false
		}
		for _, character := range identity {
			if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
				return startInput{}, false
			}
		}
		input.IdentityHash, err = hex.DecodeString(identity)
		if err != nil {
			return startInput{}, false
		}
	}
	return input, true
}
