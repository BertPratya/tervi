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
}

// List is the server's in-memory set of machines with an open connection.
type List struct {
	mu       sync.Mutex
	closeMu  sync.Mutex
	entries  map[string]entry
	handlers map[uint64]struct{}
	changed  chan struct{}
	nextID   uint64
	closed   bool
	logger   *slog.Logger
}

// NewList returns an empty connection list. A new server starts with every
// machine Offline.
func NewList() *List {
	return &List{
		entries:  make(map[string]entry),
		handlers: make(map[uint64]struct{}),
		changed:  make(chan struct{}),
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

func (list *List) trackHandler() func() {
	list.mu.Lock()
	list.nextID++
	id := list.nextID
	list.handlers[id] = struct{}{}
	list.notifyLocked()
	list.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			list.mu.Lock()
			delete(list.handlers, id)
			list.notifyLocked()
			list.mu.Unlock()
		})
	}
}

// add stores the new socket unless the list has been closed. It returns false
// for a socket accepted after shutdown began.
func (list *List) add(machineID string, conn closeable) bool {
	list.mu.Lock()
	if list.closed {
		list.mu.Unlock()
		_ = conn.Close(websocket.StatusCode(protocol.CloseServerStopping), "server stopping")
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

// CloseAll permanently closes the list, closes every stored connection, and
// waits up to five seconds for tracked handlers to finish their final writes.
func (list *List) CloseAll() {
	list.closeMu.Lock()
	defer list.closeMu.Unlock()
	deadline := time.Now().Add(5 * time.Second)

	list.mu.Lock()
	list.closed = true
	connections := make([]closeable, 0, len(list.entries))
	for _, current := range list.entries {
		connections = append(connections, current.conn)
	}
	list.entries = make(map[string]entry)
	list.notifyLocked()
	list.mu.Unlock()

	for _, conn := range connections {
		go func(conn closeable) {
			_ = conn.Close(websocket.StatusCode(protocol.CloseServerStopping), "server stopping")
		}(conn)
	}

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		list.mu.Lock()
		if len(list.handlers) == 0 {
			list.mu.Unlock()
			return
		}
		changed := list.changed
		list.mu.Unlock()
		select {
		case <-changed:
		case <-timer.C:
			return
		}
	}
}
