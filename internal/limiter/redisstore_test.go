package limiter

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisAddrForTest is the address these tests dial to check whether a local
// Redis instance is reachable.
const redisAddrForTest = "localhost:6379"

// requireRedis dials redisAddrForTest with a short timeout and skips the
// calling test (rather than failing it) if nothing answers — so `go test`
// stays green in environments with no Redis running (CI without a Redis
// service, a contributor's laptop, etc.) instead of forcing every
// contributor to run a local Redis just to run the suite.
func requireRedis(t *testing.T) *redis.Client {
	t.Helper()

	conn, err := net.DialTimeout("tcp", redisAddrForTest, 200*time.Millisecond)
	if err != nil {
		t.Skipf("redis not reachable at %s, skipping: %v", redisAddrForTest, err)
	}
	conn.Close()

	return redis.NewClient(&redis.Options{Addr: redisAddrForTest})
}

// TestRedisStore_BurstThenDeny verifies the Lua-scripted token bucket
// behaves like the in-memory one: burst 3 with a vanishingly small RPS
// (negligible refill during the test, same trick used for TokenBucket's own
// concurrency test) allows exactly 3 requests, then denies further ones.
func TestRedisStore_BurstThenDeny(t *testing.T) {
	client := requireRedis(t)
	// Register the client close FIRST so it runs LAST: t.Cleanup calls run
	// in last-added-first-called (LIFO) order, and they all run after the
	// test function itself returns — a plain `defer client.Close()` here
	// would close the connection before any t.Cleanup below got a chance to
	// use it, silently breaking key deletion (Del would fail with a
	// closed-connection error we weren't even checking).
	t.Cleanup(func() { client.Close() })

	const clientID = "redisstore-test-burst-then-deny"
	store := NewRedisStore(client, time.Hour)

	// Clean up the keys this test creates, regardless of outcome, so
	// repeated runs don't see stale state from a previous run. Every Allow
	// call also writes to statsKey now (see recordDecision), so it needs
	// cleaning up here too, not just state/config. Registered after the
	// client-close cleanup above, so it runs before it (LIFO).
	t.Cleanup(func() {
		client.Del(context.Background(), stateKey(clientID), configKey(clientID), statsKey(clientID))
	})

	const noRefillRPS = 1e-9
	if err := store.SetConfig(clientID, Config{RPS: noRefillRPS, Burst: 3, Algorithm: AlgorithmTokenBucket}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	for i := 0; i < 3; i++ {
		d := store.Allow(clientID)
		if !d.Allowed {
			t.Fatalf("request %d: got DENY, want ALLOW (decision=%+v)", i+1, d)
		}
	}

	d := store.Allow(clientID)
	if d.Allowed {
		t.Fatalf("request after burst: got ALLOW, want DENY (decision=%+v)", d)
	}
	if d.Limit != 3 {
		t.Fatalf("Limit = %v, want 3", d.Limit)
	}
}

// TestRedisStore_Stats verifies Allow increments each client's allowed/denied
// counts in Redis, and Stats() reports them back correctly.
func TestRedisStore_Stats(t *testing.T) {
	client := requireRedis(t)
	// See the comment in TestRedisStore_BurstThenDeny: register Close first
	// so it runs last (t.Cleanup is LIFO), after the key deletion below has
	// had a chance to actually use the still-open client.
	t.Cleanup(func() { client.Close() })

	const clientID = "redisstore-test-stats"
	store := NewRedisStore(client, time.Hour)

	t.Cleanup(func() {
		client.Del(context.Background(), stateKey(clientID), configKey(clientID), statsKey(clientID))
	})

	const noRefillRPS = 1e-9
	if err := store.SetConfig(clientID, Config{RPS: noRefillRPS, Burst: 2, Algorithm: AlgorithmTokenBucket}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	for i := 0; i < 2; i++ {
		if !store.Allow(clientID).Allowed {
			t.Fatalf("request %d: got DENY, want ALLOW", i+1)
		}
	}
	if store.Allow(clientID).Allowed {
		t.Fatalf("request 3: got ALLOW, want DENY")
	}

	stats := store.Stats()
	got, ok := stats[clientID]
	if !ok {
		t.Fatalf("Stats() missing entry for %q", clientID)
	}
	if got.Allowed != 2 || got.Denied != 1 {
		t.Fatalf("Stats()[%q] = %+v, want {Allowed:2 Denied:1}", clientID, got)
	}
}
