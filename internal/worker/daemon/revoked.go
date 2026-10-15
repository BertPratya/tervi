package daemon

import (
	"io"
	"net/http"
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
