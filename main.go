package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rinipd/token-bucket-rate-limiter/internal/httpapi"
	"github.com/rinipd/token-bucket-rate-limiter/internal/limiter"
	"github.com/rinipd/token-bucket-rate-limiter/internal/store"
)

// redisStateTTL bounds how long an idle client's keys live in Redis before
// expiring, so abandoned clients don't accumulate there forever.
const redisStateTTL = 1 * time.Hour

// serverConfig holds the environment-configurable settings for main.
type serverConfig struct {
	addr             string        // ADDR: address the HTTP server binds to
	backend          string        // BACKEND: "memory" (default) or "redis"
	redisAddr        string        // REDIS_ADDR: Redis address, only used when backend is "redis"
	snapshotPath     string        // SNAPSHOT_PATH: file the Manager's state is persisted to (memory backend only)
	snapshotInterval time.Duration // SNAPSHOT_INTERVAL_SECS: how often to snapshot in the background (memory backend only)
}

// loadConfig reads serverConfig from the environment, falling back to
// sensible defaults when a variable is unset or (for the interval) unparsable.
func loadConfig() serverConfig {
	cfg := serverConfig{
		addr:             ":8080",
		backend:          "memory",
		redisAddr:        "localhost:6379",
		snapshotPath:     "state.json",
		snapshotInterval: 5 * time.Second,
	}

	if v := os.Getenv("ADDR"); v != "" {
		cfg.addr = v
	}
	if v := os.Getenv("BACKEND"); v != "" {
		cfg.backend = v
	}
	if v := os.Getenv("REDIS_ADDR"); v != "" {
		cfg.redisAddr = v
	}
	if v := os.Getenv("SNAPSHOT_PATH"); v != "" {
		cfg.snapshotPath = v
	}
	if v := os.Getenv("SNAPSHOT_INTERVAL_SECS"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil {
			log.Printf("invalid SNAPSHOT_INTERVAL_SECS %q, using default %v: %v", v, cfg.snapshotInterval, err)
		} else {
			cfg.snapshotInterval = time.Duration(secs) * time.Second
		}
	}

	return cfg
}

// healthHandler responds to health-check requests with a plain-text "OK".
// It's used to prove the server is up and reachable.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// restoreState loads a prior snapshot from path (if any) into mgr. A missing
// file is expected on first run and isn't logged as an error; any other load
// failure is logged but not fatal — the server starts with empty state
// rather than refusing to start over a corrupt or unreadable snapshot.
func restoreState(mgr *limiter.Manager, path string) {
	snap, err := store.Load(path)
	if err != nil {
		log.Printf("failed to load snapshot from %s, starting with empty state: %v", path, err)
		return
	}
	mgr.Restore(snap)
	log.Printf("restored %d client config(s) and %d limiter(s) from %s", len(snap.Configs), len(snap.Limiters), path)
}

// snapshotOnce captures mgr's current state and writes it to path, logging
// the outcome. Errors are logged, not returned: a failed snapshot shouldn't
// take down the server, since the previous on-disk snapshot (or none, on
// first run) is still a valid fallback.
func snapshotOnce(mgr *limiter.Manager, path string) {
	snap := mgr.Snapshot()
	if err := store.Save(path, snap); err != nil {
		log.Printf("warning: failed to save snapshot to %s: %v", path, err)
		return
	}
	log.Printf("saved snapshot (%d config(s), %d limiter(s)) to %s", len(snap.Configs), len(snap.Limiters), path)
}

// runSnapshotter periodically snapshots mgr to path until ctx is cancelled.
// It's meant to run in its own goroutine; the periodic snapshots bound how
// much state could be lost to an unclean exit (process kill, crash) to
// roughly one interval, on top of the always-taken final snapshot on
// graceful shutdown.
func runSnapshotter(ctx context.Context, mgr *limiter.Manager, path string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			snapshotOnce(mgr, path)
		case <-ctx.Done():
			return
		}
	}
}

func main() {
	cfg := loadConfig()

	// store is whichever backend serves the HTTP handlers below. mgr is only
	// set for the memory backend — it stays nil for redis — and is used
	// afterward purely as a "which backend are we running" signal to decide
	// whether to start/run the periodic-snapshot and final-snapshot steps,
	// since Snapshot/Restore are Manager-specific and not part of the Store
	// interface (see store.go).
	var st limiter.Store
	var mgr *limiter.Manager

	switch cfg.backend {
	case "redis":
		// A Redis-backed store needs no local recovery step and no snapshot
		// loop: every Allow/SetConfig call already writes straight to Redis,
		// so Redis itself is the durable state — there is nothing here to
		// dump to a file or reload from one.
		client := redis.NewClient(&redis.Options{Addr: cfg.redisAddr})
		st = limiter.NewRedisStore(client, redisStateTTL)
		log.Printf("backend: redis (%s)", cfg.redisAddr)
	default:
		if cfg.backend != "memory" {
			log.Printf("unknown BACKEND %q, defaulting to memory", cfg.backend)
		}
		mgr = limiter.NewManager()
		// Recover any state persisted by a previous run before the server
		// starts serving traffic. Only meaningful for the memory backend.
		restoreState(mgr, cfg.snapshotPath)
		st = mgr
		log.Printf("backend: memory")
	}

	// Register the health-check route.
	// The "GET /health" pattern (Go 1.22+) matches only GET requests.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)

	// Register the admin API (PUT/GET/DELETE /admin/clients/{key}) for
	// managing per-client config. Both handlers take a limiter.Store, so
	// they work identically regardless of which backend was selected above.
	httpapi.NewAdminHandler(st).Register(mux)

	// Register the rate-limit check API (GET /check/{key}), which consumes a
	// token for the given client and reports the outcome via headers.
	httpapi.NewCheckHandler(st).Register(mux)

	// Register the stats API (GET /stats), exposing per-client allow/deny
	// counts. Works the same for either backend, since Stats is part of
	// limiter.Store.
	httpapi.NewStatsHandler(st).Register(mux)

	server := &http.Server{
		Addr:    cfg.addr,
		Handler: mux,
	}

	// ctx is cancelled the moment the process receives SIGINT (Ctrl+C) or
	// SIGTERM (e.g. `docker stop`, `kill`). Everything below that needs to
	// react to shutdown — the snapshotter and the shutdown sequence at the
	// end of main — watches this same ctx, so one signal drives the whole
	// shutdown path.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The HTTP server runs in its own goroutine because ListenAndServe
	// blocks until the server stops; main needs its goroutine free to block
	// on <-ctx.Done() instead, so it can notice the shutdown signal and start
	// the shutdown sequence below.
	go func() {
		log.Printf("server listening on %s", cfg.addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// ErrServerClosed is the expected return value once
			// server.Shutdown has been called; anything else is a real
			// failure to start/keep serving.
			log.Fatalf("server failed: %v", err)
		}
	}()

	// Snapshot in the background at the configured interval so state
	// survives an unclean exit, not just a graceful one. Only the memory
	// backend has anything to snapshot (mgr is nil for redis).
	if mgr != nil {
		go runSnapshotter(ctx, mgr, cfg.snapshotPath, cfg.snapshotInterval)
	}

	// Block until the shutdown signal arrives.
	<-ctx.Done()
	log.Printf("shutdown signal received, shutting down")

	// Give in-flight requests a bounded window to finish instead of cutting
	// them off immediately. This uses a fresh context (not ctx, which is
	// already cancelled) so Shutdown gets its own deadline rather than
	// returning instantly.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	// Take one last snapshot on the way out: the periodic snapshotter above
	// already stopped (ctx is done), so without this final snapshot, any
	// state changed since the last tick would be lost even on a clean exit.
	// Skipped entirely for redis, which persists itself as it goes.
	if mgr != nil {
		snapshotOnce(mgr, cfg.snapshotPath)
	}

	log.Printf("shutdown complete")
}
