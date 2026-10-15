package daemon

import (
	"io"
	"net/http"

	"github.com/bertpratya/tervi/internal/protocol"
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
	if isCredentialErrorBody(body, protocol.ErrorUnknownCredential) {
		return protocol.ErrorUnknownCredential
	}
	if isCredentialErrorBody(body, protocol.ErrorCredentialRevoked) {
		return protocol.ErrorCredentialRevoked
	}
	return ""
}
