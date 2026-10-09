package pairing

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/bertpratya/tervi/internal/secret"
)

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
