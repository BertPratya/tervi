package connection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bertpratya/tervi/internal/protocol"
	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/server/httpapi"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
)

type connectionHandler struct {
	deps Deps
}

func (handler connectionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	finishHandler := handler.deps.List.trackHandler()
	defer finishHandler()

	if _, hasOrigin := r.Header["Origin"]; hasOrigin {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden_origin")
		return
	}
	proof, ok := httpapi.BearerProof(r)
	if !ok {
		httpapi.WriteError(w, http.StatusUnauthorized, "unknown_credential")
		return
	}
	var machineID string
	err := handler.deps.Pool.QueryRow(r.Context(), `SELECT machine_id::text FROM machines
		WHERE credential_hash = $1 AND status = 'active'`, secret.Hash(secret.FromString(proof))).Scan(&machineID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, http.StatusUnauthorized, "unknown_credential")
		return
	}
	if err != nil {
		httpapi.WriteInternalError(w, handler.deps.Logger, "look up machine", err)
		return
	}
	if !isOpeningRequest(r) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_input")
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(protocol.MaxMessageSize)
	openedAt := time.Now()
	lastMessageReceived := openedAt
	lastSeenWrittenAt := handler.deps.Now()
	defer func() {
		elapsed := time.Since(lastMessageReceived).Seconds()
		if elapsed < 0 {
			elapsed = 0
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := handler.deps.Pool.Exec(ctx, `UPDATE machines SET last_seen_at = now() - make_interval(secs => $2)
			WHERE machine_id = $1`, machineID, elapsed); err != nil {
			logInternalError(handler.deps.Logger, "write last seen", err)
		}
	}()
	defer handler.deps.List.remove(machineID, conn)

	stored := handler.deps.List.add(machineID, conn)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	_, err = handler.deps.Pool.Exec(ctx, `UPDATE machines SET last_seen_at = now() WHERE machine_id = $1`, machineID)
	cancel()
	if err != nil {
		logInternalError(handler.deps.Logger, "write last seen", err)
		_ = conn.Close(websocket.StatusInternalError, "internal server error")
		return
	}
	if !stored {
		return
	}

	hello, err := json.Marshal(protocol.Hello{
		Type:                 protocol.TypeHello,
		CheckIntervalSeconds: int(handler.deps.CheckInterval / time.Second),
		TimeoutSeconds:       int(handler.deps.ConnectionTimeout / time.Second),
	})
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "internal server error")
		return
	}
	if err := writeMessage(conn, hello); err != nil {
		return
	}
	handler.readMessages(conn, machineID, openedAt, &lastMessageReceived, &lastSeenWrittenAt)
}

func isOpeningRequest(r *http.Request) bool {
	if !containsToken(r.Header.Values("Connection"), "upgrade") || !hasWebSocketUpgrade(r.Header.Values("Upgrade")) {
		return false
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return false
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	decoded, err := base64.StdEncoding.Strict().DecodeString(key)
	return err == nil && len(decoded) == 16 && base64.StdEncoding.EncodeToString(decoded) == key
}

func hasWebSocketUpgrade(values []string) bool {
	return len(values) == 1 && strings.EqualFold(strings.TrimSpace(values[0]), "websocket")
}

func containsToken(values []string, want string) bool {
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), want) {
				return true
			}
		}
	}
	return false
}

type readResult struct {
	messageType websocket.MessageType
	data        []byte
	err         error
	readAt      time.Time
}

func (handler connectionHandler) readMessages(conn *websocket.Conn, machineID string, openedAt time.Time, lastMessageReceived *time.Time, lastSeenWrittenAt *time.Time) {
	results := make(chan readResult, 1)
	stopReader := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			messageType, data, err := conn.Read(context.Background())
			result := readResult{messageType: messageType, data: data, err: err, readAt: time.Now()}
			select {
			case results <- result:
			case <-stopReader:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		close(stopReader)
		_ = conn.Close(websocket.StatusNormalClosure, "connection ended")
		<-readerDone
	}()

	remaining := time.Until(openedAt.Add(handler.deps.ConnectionTimeout))
	if remaining < 0 {
		remaining = 0
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			_ = conn.Close(websocket.StatusCode(protocol.CloseTimeout), "connection timed out")
			return
		case result := <-results:
			if result.err != nil {
				if websocket.CloseStatus(result.err) == websocket.StatusCode(protocol.CloseTooLarge) {
					return
				}
				return
			}
			*lastMessageReceived = result.readAt
			timer.Reset(handler.deps.ConnectionTimeout)
			if result.messageType != websocket.MessageText {
				_ = conn.Close(websocket.StatusCode(protocol.CloseInvalidMessage), "message must be a JSON text object")
				return
			}
			messageType, ok := protocol.ReadType(result.data)
			if !ok {
				_ = conn.Close(websocket.StatusCode(protocol.CloseInvalidMessage), "message must have a string type")
				return
			}
			if handler.deps.Now().Sub(*lastSeenWrittenAt) >= time.Minute {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, err := handler.deps.Pool.Exec(ctx, `UPDATE machines SET last_seen_at = now() WHERE machine_id = $1`, machineID)
				cancel()
				if err != nil {
					logInternalError(handler.deps.Logger, "write last seen", err)
					_ = conn.Close(websocket.StatusInternalError, "internal server error")
					return
				}
				*lastSeenWrittenAt = handler.deps.Now()
			}
			if messageType == protocol.TypePing {
				if err := writeMessage(conn, []byte(`{"type":"pong"}`)); err != nil {
					return
				}
			}
		}
	}
}

func writeMessage(conn *websocket.Conn, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, data)
}

func logInternalError(logger *slog.Logger, kind string, err error) {
	httpapi.WriteInternalError(discardResponseWriter{header: make(http.Header)}, logger, kind, err)
}

type discardResponseWriter struct {
	header http.Header
}

func (writer discardResponseWriter) Header() http.Header { return writer.header }

func (discardResponseWriter) WriteHeader(int) {}

func (discardResponseWriter) Write(data []byte) (int, error) { return len(data), nil }
