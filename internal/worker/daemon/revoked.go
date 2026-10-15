package daemon

import (
	"io"
	"net/http"

	"github.com/bertpratya/tervi/internal/worker/state"
)

func credentialRefusal(response *http.Response) string {
	if response == nil || response.StatusCode != http.StatusUnauthorized || response.Body == nil {
		return ""
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return ""
	}
	if isCredentialErrorBody(body, "unknown_credential") {
		return "unknown_credential"
	}
	if isCredentialErrorBody(body, "credential_revoked") {
		return "credential_revoked"
	}
	return ""
}

func (d *Daemon) revoke(server string) {
	record, exists, err := state.ReadState(d.opts.StateDir)
	if err == nil && exists {
		record.Connect = false
		reason := state.StopReasonCredentialRevoked
		record.StopReason = &reason
		_ = state.WriteState(d.opts.StateDir, record)
	}
	reason := state.StopReasonCredentialRevoked
	d.mu.Lock()
	d.enabled = false
	d.setStatusLocked(Status{State: StateNotConnecting, StopReason: &reason})
	d.mu.Unlock()
	d.logLine("✗ This computer was unpaired on the server. Pair it again with: tervi pair --server %s", server)
}
