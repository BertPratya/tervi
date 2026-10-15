package connection

import (
	"log/slog"
	"sync"
	"time"

	"github.com/bertpratya/tervi/internal/protocol"
	"github.com/coder/websocket"
)

type entry struct {
	conn closeable
}

type closeable interface {
	Close(websocket.StatusCode, string) error
	CloseNow() error
}

// List is the server's in-memory set of machines with an open connection.
type List struct {
	mu           sync.Mutex
	closeMu      sync.Mutex
	entries      map[string]entry
	handlers     map[uint64]closeable
	closePending map[closeable]struct{}
	changed      chan struct{}
	nextID       uint64
	closed       bool
	stopDeadline time.Time
	logger       *slog.Logger
}

// NewList returns an empty connection list. A new server starts with every
// machine Offline.
func NewList() *List {
	return &List{
		entries:      make(map[string]entry),
		handlers:     make(map[uint64]closeable),
		closePending: make(map[closeable]struct{}),
		changed:      make(chan struct{}),
	}
}

func (list *List) setLogger(logger *slog.Logger) {
	list.mu.Lock()
	list.logger = logger
	list.mu.Unlock()
}

func (list *List) notifyLocked() {
	close(list.changed)
	list.changed = make(chan struct{})
}

func (list *List) trackHandler() (func(closeable), func()) {
	list.mu.Lock()
	list.nextID++
	id := list.nextID
	list.handlers[id] = nil
	list.notifyLocked()
	list.mu.Unlock()

	setConnection := func(conn closeable) {
		list.mu.Lock()
		if _, exists := list.handlers[id]; exists {
			list.handlers[id] = conn
			list.notifyLocked()
		}
		list.mu.Unlock()
	}
	var once sync.Once
	finish := func() {
		once.Do(func() {
			list.mu.Lock()
			delete(list.handlers, id)
			list.notifyLocked()
			list.mu.Unlock()
		})
	}
	return setConnection, finish
}

// add stores the new socket unless the list has been closed. It returns false
// for a socket accepted after shutdown began.
func (list *List) add(machineID string, conn closeable) bool {
	list.mu.Lock()
	if list.closed {
		list.mu.Unlock()
		list.startTrackedClose(conn, websocket.StatusCode(protocol.CloseServerStopping), "server stopping")
		return false
	}
	previous, replaced := list.entries[machineID]
	list.entries[machineID] = entry{conn: conn}
	logger := list.logger
	list.notifyLocked()
	list.mu.Unlock()

	if replaced {
		if logger != nil {
			logger.Warn("machine connection replaced", "machine_id", machineID)
		}
		go func() {
			_ = previous.conn.Close(websocket.StatusCode(protocol.CloseReplaced), "replaced by a newer connection")
		}()
	}
	return true
}

func (list *List) remove(machineID string, conn closeable) {
	list.mu.Lock()
	if current, ok := list.entries[machineID]; ok && current.conn == conn {
		delete(list.entries, machineID)
		list.notifyLocked()
	}
	list.mu.Unlock()
}

// Online reports whether the machine currently has a stored connection.
func (list *List) Online(machineID string) bool {
	list.mu.Lock()
	_, ok := list.entries[machineID]
	list.mu.Unlock()
	return ok
}

// OnlineIDs returns a snapshot of machine IDs with stored connections.
func (list *List) OnlineIDs() map[string]bool {
	list.mu.Lock()
	defer list.mu.Unlock()
	ids := make(map[string]bool, len(list.entries))
	for machineID := range list.entries {
		ids[machineID] = true
	}
	return ids
}

// CloseRevoked closes the machine's connection with close code 4002, if it has
// one, and removes it from the Online list immediately.
func (list *List) CloseRevoked(machineID string) {
	list.mu.Lock()
	current, exists := list.entries[machineID]
	if !exists {
		list.mu.Unlock()
		return
	}
	delete(list.entries, machineID)
	list.closePending[current.conn] = struct{}{}
	list.notifyLocked()
	list.mu.Unlock()

	list.finishTrackedClose(current.conn, websocket.StatusCode(protocol.CloseRevoked), "credential revoked")
}

// CloseAll permanently closes the list, closes every stored connection, and
// waits up to five seconds for tracked handlers to finish their final writes.
func (list *List) CloseAll() {
	list.closeMu.Lock()
	defer list.closeMu.Unlock()

	list.mu.Lock()
	list.closed = true
	if list.stopDeadline.IsZero() {
		list.stopDeadline = time.Now().Add(5 * time.Second)
	}
	deadline := list.stopDeadline
	connections := make([]closeable, 0, len(list.entries))
	for _, current := range list.entries {
		connections = append(connections, current.conn)
		list.closePending[current.conn] = struct{}{}
	}
	list.entries = make(map[string]entry)
	list.notifyLocked()
	list.mu.Unlock()

	for _, conn := range connections {
		list.finishTrackedClose(conn, websocket.StatusCode(protocol.CloseServerStopping), "server stopping")
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		list.forceCloseHandlers()
		return
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	for {
		list.mu.Lock()
		if len(list.handlers) == 0 && len(list.closePending) == 0 {
			list.mu.Unlock()
			return
		}
		changed := list.changed
		list.mu.Unlock()
		select {
		case <-changed:
		case <-timer.C:
			list.forceCloseHandlers()
			return
		}
	}
}

func (list *List) startTrackedClose(conn closeable, code websocket.StatusCode, reason string) {
	list.mu.Lock()
	list.closePending[conn] = struct{}{}
	list.notifyLocked()
	list.mu.Unlock()
	list.finishTrackedClose(conn, code, reason)
}

func (list *List) finishTrackedClose(conn closeable, code websocket.StatusCode, reason string) {
	go func() {
		_ = conn.Close(code, reason)
		list.mu.Lock()
		delete(list.closePending, conn)
		list.notifyLocked()
		list.mu.Unlock()
	}()
}

func (list *List) forceCloseHandlers() {
	list.mu.Lock()
	connections := make(map[closeable]struct{}, len(list.handlers)+len(list.closePending))
	for _, conn := range list.handlers {
		if conn != nil {
			connections[conn] = struct{}{}
		}
	}
	for conn := range list.closePending {
		connections[conn] = struct{}{}
	}
	list.mu.Unlock()
	for conn := range connections {
		_ = conn.CloseNow()
	}
}
