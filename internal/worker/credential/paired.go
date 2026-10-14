package credential

import (
	"errors"
	"fmt"

	"github.com/bertpratya/tervi/internal/worker/state"
)

var (
	// ErrStateUnreadable identifies a state.json that could not be read or parsed.
	ErrStateUnreadable = errors.New("state.json unreadable")
)

// Paired reports whether state.json is confirmed and the credential file holds a
// usable entry. Missing state or an entry means the computer is not paired.
func Paired(stateDir string, store *Store) (entry Entry, record state.State, paired bool, err error) {
	record, exists, err := state.ReadState(stateDir)
	if err != nil {
		return Entry{}, record, false, fmt.Errorf("read state.json: %w: %w", ErrStateUnreadable, err)
	}
	if !exists || !record.Confirmed {
		return Entry{}, record, false, nil
	}
	entry, exists, err = store.Get()
	if errors.Is(err, ErrDamaged) {
		return Entry{}, record, false, ErrDamaged
	}
	if errors.Is(err, ErrUnprotected) {
		return Entry{}, record, false, err
	}
	if err != nil {
		return Entry{}, record, false, fmt.Errorf("read credential file: %w: %w", ErrStoreUnreadable, err)
	}
	if !exists {
		return Entry{}, record, false, nil
	}
	return entry, record, true, nil
}
