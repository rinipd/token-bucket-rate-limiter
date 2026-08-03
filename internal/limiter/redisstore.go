package limiter

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// tokenBucketLua implements the exact same lazy-refill token bucket
// algorithm as TokenBucket.Allow (see tokenbucket.go), but as a Redis Lua
// script instead of Go code protected by a Go sync.Mutex.
//
// Why a Lua script at all: TokenBucket's correctness depends on the whole
// "read tokens, compute refill, check, consume, write back" sequence
// happening as one atomic unit — that's what TokenBucket.mu guarantees
// in-process. A Go mutex can't protect state that lives in Redis and might
// be hit by other server instances at the same time; if we did the
// read/compute/write as separate Redis commands from Go, two instances
// could both read the same "3 tokens left", both decide to allow, and both
// write back "2 tokens left" — double-spending a token that only existed
// once. Redis executes an entire Lua script as a single atomic operation
// (it's single-threaded for command execution, and a script runs to
// completion before the next command from anyone starts), so running this
// whole sequence as one script gives us the same cross-instance atomicity a
// mutex gives us in-process, without needing a separate distributed lock.
const tokenBucketLua = `
-- KEYS[1] = the state hash key for this client (see stateKey)
-- ARGV[1] = capacity      (max tokens / burst size)
-- ARGV[2] = refillRate    (tokens added per second)
-- ARGV[3] = nowMillis     (current time, Unix milliseconds)
-- ARGV[4] = ttlSeconds    (how long to let an idle key live)
--
-- "now" MUST be passed in as an argument rather than read inside the
-- script: Redis requires scripts to be deterministic (so replication and
-- AOF replay produce identical results), and reading the wall clock from
-- inside a script would make two runs of the same script disagree.
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refillRate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local ttlSeconds = tonumber(ARGV[4])

-- Load the bucket's current state. HGET returns Lua boolean false (not nil)
-- for a missing field, and tonumber(false) is nil — so if either field is
-- absent (first request ever for this client), tokens/ts end up nil and we
-- fall into the "start full" branch below, mirroring NewTokenBucket.
local tokens = tonumber(redis.call('HGET', key, 'tokens'))
local ts = tonumber(redis.call('HGET', key, 'ts'))
if tokens == nil or ts == nil then
  tokens = capacity
  ts = now
end

-- Lazy refill, identical to TokenBucket.Allow's Go version:
--   elapsed := now - lastRefill
--   tokens  += elapsed * refillRate
--   tokens   = min(tokens, capacity)
local elapsedSecs = (now - ts) / 1000.0
tokens = math.min(capacity, tokens + elapsedSecs * refillRate)

-- Consume one token if at least one whole token is available; otherwise
-- leave the count untouched, same as TokenBucket.
local allowed = 0
if tokens >= 1 then
  allowed = 1
  tokens = tokens - 1
end

-- Persist the updated state and refresh the TTL. Storing as strings (not
-- Lua numbers) avoids Redis's integer-reply coercion on the way in, so a
-- fractional token count like 2.5 round-trips through HSET/HGET intact
-- instead of being truncated.
redis.call('HSET', key, 'tokens', tostring(tokens), 'ts', tostring(now))
redis.call('EXPIRE', key, ttlSeconds)

-- The return path is the one place Redis DOES coerce Lua numbers: script
-- replies convert Lua numbers to Redis integers, silently truncating any
-- fractional part. We make that truncation explicit and intentional with
-- math.floor rather than relying on it happening implicitly, so the
-- rounding direction is unambiguous — floor, matching TokenBucket's own
-- math.Floor(b.tokens) for its Decision.Remaining in Go.
local remaining = math.floor(tokens)
return {allowed, remaining, capacity}
`

// RedisStore is a Redis-backed Store: the same token bucket algorithm as
// TokenBucket, but with its state and atomicity provided by Redis (via
// tokenBucketLua) instead of an in-process mutex — so multiple server
// instances sharing one Redis can enforce the same limit consistently,
// which an in-memory Manager can't do across processes.
//
// Only the token_bucket algorithm is implemented here; see Allow for how a
// client configured for sliding_window is handled.
type RedisStore struct {
	client *redis.Client
	script *redis.Script
	ttl    time.Duration // how long an idle client's Redis keys are kept before expiring
}

// Compile-time assertion that *RedisStore satisfies Store, for the same
// reason Manager has one: a future signature drift fails to compile here
// instead of surfacing later as a runtime type error.
var _ Store = (*RedisStore)(nil)

// NewRedisStore returns a RedisStore backed by client, with ttl controlling
// how long an idle client's Redis keys live before expiring (bounding how
// much memory abandoned clients consume in Redis).
func NewRedisStore(client *redis.Client, ttl time.Duration) *RedisStore {
	return &RedisStore{
		client: client,
		script: redis.NewScript(tokenBucketLua),
		ttl:    ttl,
	}
}

// stateKey is the Redis hash key holding a client's live bucket state
// (tokens, ts), read and written by tokenBucketLua.
func stateKey(clientID string) string {
	return "ratelimit:state:" + clientID
}

// configKey is the Redis hash key holding a client's rate-limit Config.
func configKey(clientID string) string {
	return "ratelimit:config:" + clientID
}

// Allow reports the outcome of a request for clientID by running
// tokenBucketLua against Redis, atomically applying refill/check/consume in
// one round trip.
//
// Only token_bucket is implemented as a Redis script right now. If
// clientID's stored Config asks for sliding_window, we deliberately still
// run it through the token bucket script rather than implementing the
// sliding window algorithm in Lua (a bigger undertaking left for a future
// phase) — this is a known, intentional limitation: such a client still
// gets correctly rate-limited (same Burst/RPS numbers), just via the token
// bucket curve instead of the sliding-window one, not silently unlimited.
func (s *RedisStore) Allow(clientID string) Decision {
	cfg, ok := s.GetConfig(clientID)
	if !ok {
		cfg = DefaultConfig
	}

	ctx := context.Background()
	now := time.Now().UnixMilli()
	ttlSeconds := int64(s.ttl.Seconds())

	res, err := s.script.Run(ctx, s.client, []string{stateKey(clientID)}, cfg.Burst, cfg.RPS, now, ttlSeconds).Result()
	if err != nil {
		// Fail CLOSED (deny) on any Redis error. For a rate limiter,
		// failing OPEN (allowing everything when the backing store is
		// unreachable) defeats the limiter's whole purpose right when
		// protection matters most — a Redis outage would let unlimited
		// traffic through to whatever this is protecting. Denying is the
		// safer default here; a deployment that would rather fail open
		// under a Redis outage should invert this deliberately, with that
		// tradeoff documented at the call site.
		log.Printf("redisstore: Allow(%s): script error, failing closed (deny): %v", clientID, err)
		return Decision{Allowed: false, Limit: cfg.Burst}
	}

	values, ok := res.([]interface{})
	if !ok || len(values) != 3 {
		log.Printf("redisstore: Allow(%s): unexpected script result %#v, failing closed (deny)", clientID, res)
		return Decision{Allowed: false, Limit: cfg.Burst}
	}
	allowedInt, _ := values[0].(int64)
	remainingInt, _ := values[1].(int64)
	limitInt, _ := values[2].(int64)

	allowed := allowedInt == 1
	remaining := float64(remainingInt)
	limit := float64(limitInt)

	return Decision{
		Allowed:   allowed,
		Limit:     limit,
		Remaining: remaining,
		// RetryAfter/ResetAfter are computed here in Go, the same formulas
		// TokenBucket itself uses, but from the floored `remaining` value
		// Redis handed back rather than an exact fractional token count —
		// Redis only round-trips remaining as a floored integer through the
		// script's return path, so these are a close approximation, not the
		// exact values TokenBucket's own Decision carries.
		RetryAfter: retryAfterFromRemaining(allowed, remaining, cfg.RPS),
		ResetAfter: resetAfterFromRemaining(limit, remaining, cfg.RPS),
	}
}

// retryAfterFromRemaining mirrors TokenBucket.retryAfter, computed from a
// floored remaining-token count instead of an exact fractional one.
func retryAfterFromRemaining(allowed bool, remaining, rate float64) time.Duration {
	if allowed {
		return 0
	}
	if rate <= 0 {
		return neverRefills
	}
	seconds := (1 - remaining) / rate
	if seconds < 0 {
		seconds = 0
	}
	return time.Duration(seconds * float64(time.Second))
}

// resetAfterFromRemaining mirrors TokenBucket.resetAfter, computed from a
// floored remaining-token count instead of an exact fractional one.
func resetAfterFromRemaining(limit, remaining, rate float64) time.Duration {
	if remaining >= limit {
		return 0
	}
	if rate <= 0 {
		return neverRefills
	}
	seconds := (limit - remaining) / rate
	return time.Duration(seconds * float64(time.Second))
}

// SetConfig validates cfg and stores it as clientID's rate-limit
// configuration in Redis.
func (s *RedisStore) SetConfig(clientID string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	ctx := context.Background()
	if err := s.client.HSet(ctx, configKey(clientID), map[string]interface{}{
		"rps":       cfg.RPS,
		"burst":     cfg.Burst,
		"algorithm": cfg.Algorithm,
	}).Err(); err != nil {
		return fmt.Errorf("redisstore: SetConfig(%s): %w", clientID, err)
	}

	// Delete any existing bucket state so it rebuilds under the new limits
	// on the next Allow call — mirrors Manager.SetConfig discarding the old
	// in-memory limiter for the same reason: otherwise the client would
	// keep being rate-limited by its old capacity/rate until the state key
	// happened to expire or be recreated some other way.
	if err := s.client.Del(ctx, stateKey(clientID)).Err(); err != nil {
		return fmt.Errorf("redisstore: SetConfig(%s): delete stale state: %w", clientID, err)
	}
	return nil
}

// GetConfig returns clientID's stored config, if any.
func (s *RedisStore) GetConfig(clientID string) (Config, bool) {
	ctx := context.Background()
	fields, err := s.client.HGetAll(ctx, configKey(clientID)).Result()
	if err != nil {
		log.Printf("redisstore: GetConfig(%s): %v", clientID, err)
		return Config{}, false
	}
	if len(fields) == 0 {
		return Config{}, false
	}

	rps, _ := strconv.ParseFloat(fields["rps"], 64)
	burst, _ := strconv.ParseFloat(fields["burst"], 64)
	return Config{RPS: rps, Burst: burst, Algorithm: fields["algorithm"]}, true
}

// RemoveClient deletes clientID's config and bucket state from Redis.
func (s *RedisStore) RemoveClient(clientID string) {
	ctx := context.Background()
	if err := s.client.Del(ctx, configKey(clientID), stateKey(clientID)).Err(); err != nil {
		log.Printf("redisstore: RemoveClient(%s): %v", clientID, err)
	}
}
