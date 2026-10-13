package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"

	"github.com/bertpratya/tervi/internal/worker/credential"
)

func unknownCredential(response *http.Response) bool {
	if response == nil || response.StatusCode != http.StatusUnauthorized || response.Body == nil {
		return false
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return err == nil && isUnknownCredentialBody(body)
}

func isUnknownCredentialBody(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') || !decoder.More() {
		return false
	}
	key, err := decoder.Token()
	if err != nil || key != "error" {
		return false
	}
	var code string
	if decoder.Decode(&code) != nil || code != "unknown_credential" || decoder.More() {
		return false
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return false
	}
	var extra any
	return errors.Is(decoder.Decode(&extra), io.EOF)
}

func closeResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

func errorText(err error, response *http.Response, credentialValue string) (string, string) {
	if response != nil && response.StatusCode != http.StatusSwitchingProtocols {
		last := fmt.Sprintf("the server answered %d", response.StatusCode)
		return last, upperFirst(last)
	}
	if errors.Is(err, credential.ErrNotFound) {
		last := "the secret store entry is missing"
		return last, upperFirst(last)
	}
	if errors.Is(err, credential.ErrDamaged) {
		last := "the secret store entry is damaged"
		return last, upperFirst(last)
	}
	if isStateReadError(err) {
		return "can't read state.json", ""
	}
	if errors.Is(err, credential.ErrStoreUnreadable) {
		last := "can't read the secret store"
		return last, upperFirst(last)
	}
	if err == nil {
		return "network error", "Network error"
	}
	var opError *net.OpError
	if errors.Is(err, syscall.ECONNREFUSED) || errors.As(err, &opError) && errors.Is(opError.Err, syscall.ECONNREFUSED) {
		last := "connection refused"
		return last, upperFirst(last)
	}
	if isTimeoutError(err) {
		last := "no answer from the server"
		return last, upperFirst(last)
	}
	message := err.Error()
	if credentialValue != "" {
		message = strings.ReplaceAll(message, credentialValue, "[redacted]")
	}
	if message == "" {
		message = "network error"
	}
	return lowerFirst(message), upperFirst(message)
}

func isStateReadError(err error) bool {
	return errors.Is(err, credential.ErrStateUnreadable)
}

func lowerFirst(value string) string {
	if value == "" {
		return value
	}
	return strings.ToLower(value[:1]) + value[1:]
}

func upperFirst(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
