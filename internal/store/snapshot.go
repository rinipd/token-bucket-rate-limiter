// Package store persists limiter.PersistedState snapshots to disk.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rinipd/token-bucket-rate-limiter/internal/limiter"
)

// Save writes s to path as indented JSON.
//
// The write is atomic: we marshal and write to a temp file in the same
// directory as path, then os.Rename it over path. Rename is an atomic
// replace on POSIX filesystems, so a reader (or a crash) never observes a
// partially-written file — path either still holds the previous snapshot or
// the complete new one, never a truncated mix of both. The temp file lives
// in the same directory so the rename is guaranteed to stay on one
// filesystem (renames across filesystems aren't atomic, and may fail).
func Save(path string, s limiter.PersistedState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp snapshot file: %w", err)
	}
	tmpPath := tmp.Name()
	// Clean up the temp file if we bail out before the rename succeeds.
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp snapshot file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp snapshot file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp snapshot file into place: %w", err)
	}
	return nil
}

// Load reads and unmarshals the PersistedState stored at path. If path
// doesn't exist, Load returns an empty PersistedState and a nil error: the
// first run of the process has no prior snapshot to restore, and that's not
// a failure.
func Load(path string) (limiter.PersistedState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return limiter.PersistedState{}, nil
		}
		return limiter.PersistedState{}, fmt.Errorf("read snapshot file: %w", err)
	}

	var s limiter.PersistedState
	if err := json.Unmarshal(data, &s); err != nil {
		return limiter.PersistedState{}, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return s, nil
}
