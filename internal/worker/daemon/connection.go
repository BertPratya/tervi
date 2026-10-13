package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/bertpratya/tervi/internal/protocol"
	"github.com/bertpratya/tervi/internal/worker/credential"
	"github.com/bertpratya/tervi/internal/worker/state"
	"github.com/coder/websocket"
)

type attemptKind uint8

const (
	attemptIdle attemptKind = iota
	attemptFailed
	attemptRejected
	attemptConnected
)

type attemptResult struct {
	kind      attemptKind
	server    string
	lastError string
	logError  string
	conn      *websocket.Conn
	hello     protocol.Hello
	record    state.State
}

type lostResult struct {
	reason       string
	closeStarted bool
}

func (d *Daemon) tryConnection(parent context.Context, try int) attemptResult {
	entry, record, paired, err := credential.Paired(d.opts.StateDir, d.opts.Store)
	if isStateReadError(err) {
		d.refreshState()
		return attemptResult{kind: attemptIdle}
	}
	if !record.Confirmed {
		d.refreshState()
		return attemptResult{kind: attemptIdle}
	}
	if !record.Connect {
		d.refreshState()
		return attemptResult{kind: attemptIdle}
	}
	if errors.Is(err, credential.ErrDamaged) {
		return d.failedAttempt("", credential.ErrDamaged, nil, "")
	}
	if err != nil {
		return d.failedAttempt("", err, nil, "")
	}
	if !paired {
		return d.failedAttempt("", credential.ErrNotFound, nil, "")
	}
	server := entry.Server
	d.setTrying(try, server, d.opts.Now())

	connectionURL, urlErr := connectionAddress(server)
	if urlErr != nil {
		return d.failedAttempt(server, urlErr, nil, entry.Credential.Reveal())
	}
	dialCtx, cancel := context.WithTimeout(parent, 10*time.Second)
	conn, response, dialErr := d.opts.Dial(dialCtx, connectionURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + entry.Credential.Reveal()}},
	})
	cancel()
	if parent.Err() != nil {
		if conn != nil {
			_ = conn.Close(websocket.StatusCode(protocol.CloseNormal), "daemon stopped")
		}
		return attemptResult{kind: attemptIdle}
	}
	if unknownCredential(response) {
		closeResponse(response)
		if conn != nil {
			_ = conn.CloseNow()
		}
		return attemptResult{kind: attemptRejected, server: server, record: record}
	}
	if dialErr != nil || conn == nil {
		if conn != nil {
			_ = conn.CloseNow()
		}
		if dialErr == nil {
			dialErr = errors.New("websocket dial returned no connection")
		}
		result := d.failedAttempt(server, dialErr, nil, entry.Credential.Reveal())
		if response != nil {
			result.lastError, result.logError = errorText(dialErr, response, entry.Credential.Reveal())
			closeResponse(response)
		}
		return result
	}
	if response != nil && response.StatusCode != http.StatusSwitchingProtocols {
		closeResponse(response)
		_ = conn.CloseNow()
		return d.failedAttempt(server, nil, response, entry.Credential.Reveal())
	}
	conn.SetReadLimit(protocol.MaxMessageSize)

	hello, helloErr := readHello(parent, conn)
	if helloErr != nil {
		if parent.Err() != nil {
			_ = conn.Close(websocket.StatusCode(protocol.CloseNormal), "daemon stopped")
			return attemptResult{kind: attemptIdle}
		}
		if isTimeoutError(helloErr) {
			go func() {
				_ = conn.Close(websocket.StatusCode(protocol.CloseTimeout), "hello timeout")
			}()
		} else {
			_ = conn.CloseNow()
		}
		return d.failedAttempt(server, helloErr, nil, entry.Credential.Reveal())
	}
	return attemptResult{kind: attemptConnected, server: server, conn: conn, hello: hello}
}

func (d *Daemon) failedAttempt(server string, err error, response *http.Response, credentialValue string) attemptResult {
	last, logged := errorText(err, response, credentialValue)
	return attemptResult{kind: attemptFailed, server: server, lastError: last, logError: logged}
}

func readHello(ctx context.Context, conn *websocket.Conn) (protocol.Hello, error) {
	helloCtx, cancel := context.WithTimeout(ctx, protocol.DefaultHelloTimeout)
	defer cancel()
	for {
		_, data, err := conn.Read(helloCtx)
		if err != nil {
			return protocol.Hello{}, err
		}
		messageType, ok := protocol.ReadType(data)
		if !ok || messageType != protocol.TypeHello {
			continue
		}
		var hello protocol.Hello
		if err := json.Unmarshal(data, &hello); err != nil || hello.CheckIntervalSeconds < 1 || hello.TimeoutSeconds < 1 {
			continue
		}
		return hello, nil
	}
}

func (d *Daemon) serveConnection(ctx context.Context, conn *websocket.Conn, hello protocol.Hello) lostResult {
	interval := time.Duration(hello.CheckIntervalSeconds) * time.Second
	timeout := time.Duration(hello.TimeoutSeconds) * time.Second
	pingTimer := time.NewTimer(interval)
	defer pingTimer.Stop()
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()
	lastMessageAt := time.Now()
	type readResult struct {
		data []byte
		err  error
	}
	reads := make(chan readResult, 1)
	startRead := func() {
		go func() {
			_, data, err := conn.Read(context.Background())
			reads <- readResult{data: data, err: err}
		}()
	}
	startRead()
	for {
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusCode(protocol.CloseNormal), "daemon stopped")
			return lostResult{reason: "the server closed the connection", closeStarted: true}
		case <-timeoutTimer.C:
			return closeForTimeout(conn, hello.TimeoutSeconds)
		case <-pingTimer.C:
			writeTimeout := timeout - time.Since(lastMessageAt)
			if writeTimeout <= 0 {
				return closeForTimeout(conn, hello.TimeoutSeconds)
			}
			writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := conn.Write(writeCtx, websocket.MessageText, []byte(`{"type":"ping"}`))
			cancel()
			if err != nil {
				if isTimeoutError(err) && ctx.Err() == nil {
					return closeForTimeout(conn, hello.TimeoutSeconds)
				}
				return connectionLost(err)
			}
			pingTimer.Reset(interval)
		case read := <-reads:
			if read.err != nil {
				return connectionLost(read.err)
			}
			lastMessageAt = time.Now()
			resetTimer(timeoutTimer, timeout)
			startRead()
		}
	}
}

func closeForTimeout(conn *websocket.Conn, seconds int) lostResult {
	go func() {
		_ = conn.Close(websocket.StatusCode(protocol.CloseTimeout), "no message received before timeout")
	}()
	return lostResult{
		reason:       fmt.Sprintf("no answer from the server for %d seconds", seconds),
		closeStarted: true,
	}
}

func connectionLost(err error) lostResult {
	code := websocket.CloseStatus(err)
	switch code {
	case websocket.StatusCode(protocol.CloseServerStopping):
		return lostResult{reason: "the server is stopping"}
	case websocket.StatusCode(protocol.CloseReplaced):
		return lostResult{reason: "replaced by a newer connection"}
	case -1:
		last, _ := errorText(err, nil, "")
		return lostResult{reason: last}
	default:
		return lostResult{reason: "the server closed the connection"}
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

func connectionAddress(server string) (string, error) {
	parsed, err := url.Parse(server)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		return "", errors.New("unsupported server scheme")
	}
	parsed.Path = "/api/v1/machines/current/connection"
	parsed.RawPath = ""
	return parsed.String(), nil
}
