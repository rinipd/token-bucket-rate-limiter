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
// in-flight bucket token counts survive a Snapshot into a fresh Manager via
// Restore.
func TestManager_SnapshotRestoreRoundTrip(t *testing.T) {
	m := NewManager()

	if err := m.SetConfig("alice", Config{RPS: 1, Burst: 5, Algorithm: AlgorithmTokenBucket}); err != nil {
		t.Fatalf("SetConfig(alice): %v", err)
	}
	if err := m.SetConfig("bob", Config{RPS: 2, Burst: 10, Algorithm: AlgorithmTokenBucket}); err != nil {
		t.Fatalf("SetConfig(bob): %v", err)
	}

	// Partially drain alice's bucket so its token count is neither full nor
	// empty, and leave bob's config with no bucket created yet.
	for i := 0; i < 3; i++ {
		if !m.Allow("alice").Allowed {
			t.Fatalf("Allow(alice) request %d: got DENY, want ALLOW", i+1)
		}
	}

	snap := m.Snapshot()

	if len(snap.Configs) != 2 {
		t.Fatalf("snapshot has %d configs, want 2", len(snap.Configs))
	}
	if len(snap.Buckets) != 1 {
		t.Fatalf("snapshot has %d buckets, want 1 (only alice's was created)", len(snap.Buckets))
	}
	aliceState, ok := snap.Buckets["alice"]
	if !ok {
		t.Fatalf("snapshot missing alice's bucket state")
	}
	// Buckets are wired to the real clock, so a small, real amount of refill
	// accrues between the three Allow calls above — compare with tolerance
	// rather than requiring bit-exact equality against a real clock.
	if math.Abs(aliceState.Tokens-2) > tokenTolerance {
		t.Fatalf("alice's snapshotted tokens = %v, want ~2 (5 burst - 3 consumed)", aliceState.Tokens)
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

	restoredSnap := restored.Snapshot()
	restoredAliceState, ok := restoredSnap.Buckets["alice"]
	if !ok {
		t.Fatalf("restored manager missing alice's bucket state")
	}
	if restoredAliceState.Tokens != aliceState.Tokens {
		t.Fatalf("restored alice tokens = %v, want %v", restoredAliceState.Tokens, aliceState.Tokens)
	}
	if restoredAliceState.Capacity != aliceState.Capacity || restoredAliceState.RefillRate != aliceState.RefillRate {
		t.Fatalf("restored alice bucket state = %+v, want capacity/refillRate matching %+v", restoredAliceState, aliceState)
	}
}
