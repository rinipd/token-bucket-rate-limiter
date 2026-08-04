package limiter

import "testing"

// TestManager_Stats verifies Allow updates each client's allowed/denied
// counts, and Stats() reports them.
func TestManager_Stats(t *testing.T) {
	m := NewManager()

	const clientID = "stats-test"
	// Vanishingly small RPS, same trick used elsewhere: negligible refill
	// during the test keeps the burst-then-deny outcome deterministic.
	const noRefillRPS = 1e-9
	if err := m.SetConfig(clientID, Config{RPS: noRefillRPS, Burst: 2, Algorithm: AlgorithmTokenBucket}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	for i := 0; i < 2; i++ {
		if !m.Allow(clientID).Allowed {
			t.Fatalf("request %d: got DENY, want ALLOW", i+1)
		}
	}
	if m.Allow(clientID).Allowed {
		t.Fatalf("request 3: got ALLOW, want DENY")
	}

	stats := m.Stats()
	got, ok := stats[clientID]
	if !ok {
		t.Fatalf("Stats() missing entry for %q", clientID)
	}
	if got.Allowed != 2 || got.Denied != 1 {
		t.Fatalf("Stats()[%q] = %+v, want {Allowed:2 Denied:1}", clientID, got)
	}
}
