package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rinipd/token-bucket-rate-limiter/internal/limiter"
)

// TestSaveLoadRoundTrip verifies that Save followed by Load reproduces the
// original PersistedState via a real file on disk, covering both tagged
// LimiterState shapes (token_bucket and sliding_window).
func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")

	tbState := limiter.BucketState{
		Tokens:     2.5,
		LastRefill: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		Capacity:   5,
		RefillRate: 1,
	}
	swState := limiter.SlidingWindowState{
		Limit:         5,
		Window:        time.Second,
		WindowStart:   time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		CurrentCount:  2,
		PreviousCount: 1,
	}

	want := limiter.PersistedState{
		Configs: map[string]limiter.Config{
			"alice": {RPS: 1, Burst: 5, Algorithm: limiter.AlgorithmTokenBucket},
			"carol": {RPS: 5, Burst: 5, Algorithm: limiter.AlgorithmSlidingWindow},
		},
		Limiters: map[string]limiter.LimiterState{
			"alice": {Algorithm: limiter.AlgorithmTokenBucket, TokenBucket: &tbState},
			"carol": {Algorithm: limiter.AlgorithmSlidingWindow, SlidingWindow: &swState},
		},
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(got.Configs) != 2 || got.Configs["alice"] != want.Configs["alice"] || got.Configs["carol"] != want.Configs["carol"] {
		t.Fatalf("loaded configs = %+v, want %+v", got.Configs, want.Configs)
	}

	gotAlice, ok := got.Limiters["alice"]
	if !ok || gotAlice.Algorithm != limiter.AlgorithmTokenBucket || gotAlice.TokenBucket == nil {
		t.Fatalf("loaded alice limiter state = %+v, want a token_bucket state", gotAlice)
	}
	if !gotAlice.TokenBucket.LastRefill.Equal(tbState.LastRefill) ||
		gotAlice.TokenBucket.Tokens != tbState.Tokens ||
		gotAlice.TokenBucket.Capacity != tbState.Capacity ||
		gotAlice.TokenBucket.RefillRate != tbState.RefillRate {
		t.Fatalf("loaded alice bucket state = %+v, want %+v", gotAlice.TokenBucket, tbState)
	}

	gotCarol, ok := got.Limiters["carol"]
	if !ok || gotCarol.Algorithm != limiter.AlgorithmSlidingWindow || gotCarol.SlidingWindow == nil {
		t.Fatalf("loaded carol limiter state = %+v, want a sliding_window state", gotCarol)
	}
	if !gotCarol.SlidingWindow.WindowStart.Equal(swState.WindowStart) ||
		gotCarol.SlidingWindow.Limit != swState.Limit ||
		gotCarol.SlidingWindow.Window != swState.Window ||
		gotCarol.SlidingWindow.CurrentCount != swState.CurrentCount ||
		gotCarol.SlidingWindow.PreviousCount != swState.PreviousCount {
		t.Fatalf("loaded carol sliding window state = %+v, want %+v", gotCarol.SlidingWindow, swState)
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
	if len(got.Configs) != 0 || len(got.Limiters) != 0 {
		t.Fatalf("Load(missing file) = %+v, want empty PersistedState", got)
	}
}
