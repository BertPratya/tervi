package pair

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"time"
)

type requestResult struct {
	statusCode       int
	body             []byte
	wroteRequest     bool
	receivedResponse bool
	err              error
}

func (f *pairingFlow) request(ctx context.Context, method, url, bearer string, body []byte) requestResult {
	return f.requestWithTimeout(ctx, method, url, bearer, body, f.requestTimeout)
}

func (f *pairingFlow) requestUntil(ctx context.Context, method, url, bearer string, body []byte, deadline time.Time) requestResult {
	timeout := deadline.Sub(f.now())
	if timeout > f.requestTimeout {
		timeout = f.requestTimeout
	}
	return f.requestWithTimeout(ctx, method, url, bearer, body, timeout)
}

func (f *pairingFlow) requestWithTimeout(ctx context.Context, method, url, bearer string, body []byte, timeout time.Duration) requestResult {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, url, bytesReader(body))
	if err != nil {
		return requestResult{err: err}
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	var wrote atomic.Bool
	trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
		if info.Err == nil {
			wrote.Store(true)
		}
	}}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	response, err := f.client.Do(request)
	if err != nil {
		return requestResult{wroteRequest: wrote.Load(), err: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err == nil && len(data) > maxResponseSize {
		err = errors.New("response too large")
	}
	return requestResult{statusCode: response.StatusCode, body: data, wroteRequest: wrote.Load(), receivedResponse: true, err: err}
}

func bytesReader(body []byte) io.Reader {
	if body == nil {
		return nil
	}
	return strings.NewReader(string(body))
}

type startResponse struct {
	PollingKey       string `json:"polling_key"`
	ApprovalURL      string `json:"approval_url"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

func decodeStartResponse(body []byte) (startResponse, error) {
	var response startResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return startResponse{}, err
	}
	if response.PollingKey == "" || response.ApprovalURL == "" || response.ExpiresInSeconds <= 0 {
		return startResponse{}, errors.New("incomplete start response")
	}
	return response, nil
}
