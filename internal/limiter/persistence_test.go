package limiter

import (
	"math"
	"testing"
)

// tokenTolerance accounts for the small, real amount of refill that
// necessarily accrues between successive Allow calls against a real-clock
// bucket (Manager always wires buckets to time.Now). It's generous enough to
// absorb scheduling jitter (e.g. under -race) while still catching a
// meaningfully wrong token count.
const tokenTolerance = 0.05

// TestManager_SnapshotRestoreRoundTrip verifies that a Manager's configs and
// in-flight limiter states survive a Snapshot into a fresh Manager via
// Restore, for BOTH algorithms: a token-bucket client and a sliding-window
// client must each come back as the correct concrete type with matching
// state.
func TestManager_SnapshotRestoreRoundTrip(t *testing.T) {
	m := NewManager()

	if err := m.SetConfig("alice", Config{RPS: 1, Burst: 5, Algorithm: AlgorithmTokenBucket}); err != nil {
		t.Fatalf("SetConfig(alice): %v", err)
	}
	if err := m.SetConfig("bob", Config{RPS: 2, Burst: 10, Algorithm: AlgorithmTokenBucket}); err != nil {
		t.Fatalf("SetConfig(bob): %v", err)
	}
	if err := m.SetConfig("carol", Config{RPS: 5, Burst: 5, Algorithm: AlgorithmSlidingWindow}); err != nil {
		t.Fatalf("SetConfig(carol): %v", err)
	}

	// Partially drain alice's token bucket so its token count is neither full
	// nor empty, and leave bob's config with no limiter created yet.
	for i := 0; i < 3; i++ {
		if !m.Allow("alice").Allowed {
			t.Fatalf("Allow(alice) request %d: got DENY, want ALLOW", i+1)
		}
	}
	// Partially consume carol's sliding window the same way.
	for i := 0; i < 2; i++ {
		if !m.Allow("carol").Allowed {
			t.Fatalf("Allow(carol) request %d: got DENY, want ALLOW", i+1)
		}
	}

	snap := m.Snapshot()

	if len(snap.Configs) != 3 {
		t.Fatalf("snapshot has %d configs, want 3", len(snap.Configs))
	}
	if len(snap.Limiters) != 2 {
		t.Fatalf("snapshot has %d limiters, want 2 (only alice's and carol's were created)", len(snap.Limiters))
	}

	aliceState, ok := snap.Limiters["alice"]
	if !ok || aliceState.Algorithm != AlgorithmTokenBucket || aliceState.TokenBucket == nil {
		t.Fatalf("snapshot missing/incorrect alice's limiter state: %+v", aliceState)
	}
	// Buckets are wired to the real clock, so a small, real amount of refill
	// accrues between the three Allow calls above — compare with tolerance
	// rather than requiring bit-exact equality against a real clock.
	if math.Abs(aliceState.TokenBucket.Tokens-2) > tokenTolerance {
		t.Fatalf("alice's snapshotted tokens = %v, want ~2 (5 burst - 3 consumed)", aliceState.TokenBucket.Tokens)
	}

	carolState, ok := snap.Limiters["carol"]
	if !ok || carolState.Algorithm != AlgorithmSlidingWindow || carolState.SlidingWindow == nil {
		t.Fatalf("snapshot missing/incorrect carol's limiter state: %+v", carolState)
	}
	if carolState.SlidingWindow.CurrentCount != 2 {
		t.Fatalf("carol's snapshotted currentCount = %v, want 2", carolState.SlidingWindow.CurrentCount)
	}

	restored := NewManager()
	restored.Restore(snap)

	aliceCfg, ok := restored.GetConfig("alice")
	if !ok || aliceCfg != snap.Configs["alice"] {
		t.Fatalf("restored alice config = %+v, ok=%v; want %+v, ok=true", aliceCfg, ok, snap.Configs["alice"])
	}
	bobCfg, ok := restored.GetConfig("bob")
	if !ok || bobCfg != snap.Configs["bob"] {
		t.Fatalf("restored bob config = %+v, ok=%v; want %+v, ok=true", bobCfg, ok, snap.Configs["bob"])
	}
	carolCfg, ok := restored.GetConfig("carol")
	if !ok || carolCfg != snap.Configs["carol"] {
		t.Fatalf("restored carol config = %+v, ok=%v; want %+v, ok=true", carolCfg, ok, snap.Configs["carol"])
	}

	restoredSnap := restored.Snapshot()

	restoredAliceState, ok := restoredSnap.Limiters["alice"]
	if !ok || restoredAliceState.TokenBucket == nil {
		t.Fatalf("restored manager missing alice's limiter state")
	}
	if restoredAliceState.TokenBucket.Tokens != aliceState.TokenBucket.Tokens {
		t.Fatalf("restored alice tokens = %v, want %v", restoredAliceState.TokenBucket.Tokens, aliceState.TokenBucket.Tokens)
	}
	if restoredAliceState.TokenBucket.Capacity != aliceState.TokenBucket.Capacity ||
		restoredAliceState.TokenBucket.RefillRate != aliceState.TokenBucket.RefillRate {
		t.Fatalf("restored alice bucket state = %+v, want capacity/refillRate matching %+v",
			restoredAliceState.TokenBucket, aliceState.TokenBucket)
	}

	restoredCarolState, ok := restoredSnap.Limiters["carol"]
	if !ok || restoredCarolState.SlidingWindow == nil {
		t.Fatalf("restored manager missing carol's limiter state")
	}
	if restoredCarolState.SlidingWindow.CurrentCount != carolState.SlidingWindow.CurrentCount ||
		restoredCarolState.SlidingWindow.PreviousCount != carolState.SlidingWindow.PreviousCount ||
		restoredCarolState.SlidingWindow.Limit != carolState.SlidingWindow.Limit ||
		restoredCarolState.SlidingWindow.Window != carolState.SlidingWindow.Window {
		t.Fatalf("restored carol sliding window state = %+v, want matching %+v",
			restoredCarolState.SlidingWindow, carolState.SlidingWindow)
	}
}
