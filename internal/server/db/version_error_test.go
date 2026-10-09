package db

import (
	"errors"
	"testing"
)

func TestVersionError(t *testing.T) {
	tests := []struct {
		name      string
		dbVersion int64
		newest    int64
		want      string
		wantIs    error
	}{
		{
			name:      "not migrated",
			dbVersion: 0,
			newest:    1,
			want:      ErrNotMigrated.Error(),
			wantIs:    ErrNotMigrated,
		},
		{
			name:      "older",
			dbVersion: 1,
			newest:    2,
			want:      `database is at version 1, this program needs version 2: run "server migrate" first`,
			wantIs:    ErrVersionMismatch,
		},
		{
			name:      "same",
			dbVersion: 1,
			newest:    1,
		},
		{
			name:      "newer",
			dbVersion: 3,
			newest:    1,
			want:      `database is at version 3, newer than this program (version 1): use a newer program`,
			wantIs:    ErrVersionMismatch,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := versionError(tt.dbVersion, tt.newest)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("versionError() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.want {
				t.Fatalf("versionError() = %v, want %q", err, tt.want)
			}
			if !errors.Is(err, tt.wantIs) {
				t.Errorf("errors.Is(versionError(), %v) = false", tt.wantIs)
			}
		})
	}
}
