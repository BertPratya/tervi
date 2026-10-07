package local

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// State is the non-secret local record used to recover an interrupted pairing.
type State struct {
	MachineID       string `json:"machine_id"`
	CredentialSaved bool   `json:"credential_saved"`
	Confirmed       bool   `json:"confirmed"`
}

// StatePath returns the path to the worker state file in dir.
func StatePath(dir string) string { return filepath.Join(dir, "state.json") }

// ReadState reads state.json. exists is false only when the file is missing.
func ReadState(dir string) (state State, exists bool, err error) {
	data, err := os.ReadFile(StatePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, true, err
	}
	return state, true, nil
}

// WriteState atomically replaces state.json with private, synced contents.
func WriteState(dir string, state State) error {
	_, statErr := os.Stat(dir)
	created := errors.Is(statErr, os.ErrNotExist)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if created {
		if err := os.Chmod(dir, 0700); err != nil {
			return err
		}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, StatePath(dir)); err != nil {
		return err
	}
	return nil
}

// DeleteState removes state.json; a missing file is not an error.
func DeleteState(dir string) error {
	err := os.Remove(StatePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete state: %w", err)
	}
	return nil
}
