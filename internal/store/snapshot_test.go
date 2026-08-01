package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rinipd/token-bucket-rate-limiter/internal/limiter"
)

// TestSaveLoadRoundTrip verifies that Save followed by Load reproduces the
// original PersistedState via a real file on disk.
func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")

	want := limiter.PersistedState{
		Configs: map[string]limiter.Config{
			"alice": {RPS: 1, Burst: 5, Algorithm: limiter.AlgorithmTokenBucket},
		},
		Buckets: map[string]limiter.BucketState{
			"alice": {
				Tokens:     2.5,
				LastRefill: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
				Capacity:   5,
				RefillRate: 1,
			},
		},
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(got.Configs) != 1 || got.Configs["alice"] != want.Configs["alice"] {
		t.Fatalf("loaded configs = %+v, want %+v", got.Configs, want.Configs)
	}
	gotBucket, ok := got.Buckets["alice"]
	wantBucket := want.Buckets["alice"]
	if !ok || !gotBucket.LastRefill.Equal(wantBucket.LastRefill) ||
		gotBucket.Tokens != wantBucket.Tokens ||
		gotBucket.Capacity != wantBucket.Capacity ||
		gotBucket.RefillRate != wantBucket.RefillRate {
		t.Fatalf("loaded bucket state = %+v, want %+v", gotBucket, wantBucket)
	}
}

// TestLoad_MissingFile verifies that loading a snapshot that has never been
// written (e.g. the process's first run) yields an empty state and no
// error, rather than failing.
func TestLoad_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load(missing file): got error %v, want nil", err)
	}
	if len(got.Configs) != 0 || len(got.Buckets) != 0 {
		t.Fatalf("Load(missing file) = %+v, want empty PersistedState", got)
	}
}
