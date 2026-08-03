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
	defer client.Close()

	const clientID = "redisstore-test-burst-then-deny"
	store := NewRedisStore(client, time.Hour)

	// Clean up the keys this test creates, regardless of outcome, so
	// repeated runs don't see stale state from a previous run.
	t.Cleanup(func() {
		client.Del(context.Background(), stateKey(clientID), configKey(clientID))
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
