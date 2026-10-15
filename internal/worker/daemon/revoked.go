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
	if isCredentialErrorBody(body, credentialErrorUnknown) {
		return credentialErrorUnknown
	}
	if isCredentialErrorBody(body, credentialErrorRevoked) {
		return credentialErrorRevoked
	}
	return ""
}

func (d *Daemon) revoke(server string) {
	d.stopTrying(state.StopReasonCredentialRevoked)
	d.logLine("✗ This computer was unpaired on the server. Pair it again with: tervi pair --server %s", server)
}
