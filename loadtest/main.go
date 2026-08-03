// Command loadtest is a standalone load-testing tool for the
// token-bucket-rate-limiter HTTP server. It hammers a single client's
// GET /check/{key} endpoint with concurrent requests and reports either:
//
//   - correctness: did the limiter allow (HTTP 200) exactly the number of
//     requests it should have, no more?
//   - throughput: how fast can the server serve /check under concurrent
//     load, and what does the latency distribution look like?
//
// This is a separate program from the server (its own package main, in its
// own directory) and talks to it purely over HTTP, the same way any real
// caller would — it imports nothing from the server or limiter packages.
//
// Example usage:
//
//	# Start the server first, in another terminal:
//	go run .
//
//	# Configure the client this tool will hammer, e.g. burst 100:
//	curl -X PUT localhost:8080/admin/clients/loadtest \
//	  -d '{"RPS":1,"Burst":100,"Algorithm":"token_bucket"}'
//
//	# Correctness: fire 5000 requests at 50 workers and expect exactly 100
//	# ALLOWs (the burst capacity), no more:
//	go run ./loadtest -mode correctness -key loadtest -n 5000 -c 50 -expect 100
//
//	# Throughput: fire 20000 requests at 200 concurrent workers and report
//	# achieved req/sec and latency percentiles:
//	go run ./loadtest -mode throughput -key loadtest -n 20000 -c 200
package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	urlFlag := flag.String("url", "http://localhost:8080", "base URL of the running limiter server")
	keyFlag := flag.String("key", "loadtest", "client key to hammer")
	nFlag := flag.Int64("n", 5000, "total number of requests to send")
	cFlag := flag.Int("c", 50, "concurrency (number of worker goroutines)")
	modeFlag := flag.String("mode", "correctness", "mode: correctness or throughput")
	expectFlag := flag.Int64("expect", 0, "correctness mode: expected number of allowed requests (0 = don't assert, just report)")
	flag.Parse()

	if *nFlag <= 0 || *cFlag <= 0 {
		fmt.Fprintln(os.Stderr, "-n and -c must both be > 0")
		os.Exit(2)
	}

	targetURL := strings.TrimRight(*urlFlag, "/") + "/check/" + *keyFlag

	// One http.Client, shared across every worker goroutine. http.Client is
	// safe for concurrent use, and creating a fresh client per request would
	// mean a fresh (non-pooled) *http.Transport each time — no connection
	// reuse, so every single request pays a new TCP (and possibly TLS)
	// handshake instead of reusing a keep-alive connection. That would both
	// exhaust ephemeral sockets under load and make the throughput numbers
	// measure connection setup instead of the server's actual request
	// handling.
	//
	// MaxIdleConnsPerHost is raised to at least the worker count so that with
	// -c workers all keeping a connection alive simultaneously, the pool
	// doesn't force connections to be closed and reopened.
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        1000,
			MaxIdleConnsPerHost: max(*cFlag, 100),
			IdleConnTimeout:     90 * time.Second,
		},
	}

	allowed, denied, errs, latencies, elapsed := runWorkers(*nFlag, *cFlag, targetURL, client)

	switch *modeFlag {
	case "correctness":
		runCorrectness(*nFlag, *expectFlag, allowed, denied, errs, elapsed)
	case "throughput":
		runThroughput(*nFlag, allowed, denied, errs, latencies, elapsed)
	default:
		fmt.Fprintf(os.Stderr, "unknown -mode %q (want %q or %q)\n", *modeFlag, "correctness", "throughput")
		os.Exit(2)
	}
}

// runWorkers fires n GET requests at targetURL across c concurrent worker
// goroutines, as fast as they'll go (max pressure), and returns the tallies,
// every request's latency, and the total wall-clock time taken.
//
// Work is distributed via a single shared atomic counter (sent): each worker
// loops, atomically claiming the next request index until the counter
// passes n, rather than pre-splitting n/c requests per worker — this way a
// slow worker (e.g. stuck behind a slow connection) doesn't leave requests
// undone while other workers sit idle.
//
// allowed/denied/errs are tallied with atomic adds because all c worker
// goroutines increment them concurrently; a plain int64 would race.
//
// Each worker appends its request latencies to a slice it alone owns
// (perWorkerLatencies[workerID]) instead of every worker appending to one
// shared slice. Appending to a shared slice from multiple goroutines needs
// its own lock (append itself isn't safe for concurrent use, and a shared
// mutex would serialize every single request across all workers, right in
// the hot path we're trying to measure). Giving each worker its own slice
// means zero contention during the run; the slices are only merged together
// once, after wg.Wait(), when every goroutine is done.
func runWorkers(n int64, c int, targetURL string, client *http.Client) (allowed, denied, errs int64, latencies []time.Duration, elapsed time.Duration) {
	var sent int64
	perWorkerLatencies := make([][]time.Duration, c)

	var wg sync.WaitGroup
	start := time.Now()

	for w := 0; w < c; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			var local []time.Duration
			defer func() { perWorkerLatencies[workerID] = local }()

			for {
				if atomic.AddInt64(&sent, 1) > n {
					return
				}

				reqStart := time.Now()
				resp, err := client.Get(targetURL)
				dur := time.Since(reqStart)

				if err != nil {
					atomic.AddInt64(&errs, 1)
					continue
				}

				// Drain and close the body so the underlying connection can
				// be reused by the pool; leaving it unread/unclosed forces
				// the transport to open a new connection for the next
				// request instead of reusing this one.
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				switch resp.StatusCode {
				case http.StatusOK:
					atomic.AddInt64(&allowed, 1)
				case http.StatusTooManyRequests:
					atomic.AddInt64(&denied, 1)
				default:
					atomic.AddInt64(&errs, 1)
				}
				local = append(local, dur)
			}
		}(w)
	}

	wg.Wait()
	elapsed = time.Since(start)

	for _, l := range perWorkerLatencies {
		latencies = append(latencies, l...)
	}
	return allowed, denied, errs, latencies, elapsed
}

// runCorrectness prints the request tallies and, if expect > 0, asserts that
// the number allowed matches it (within a small tolerance) and exits
// non-zero on failure so this can be used as a scripted check (e.g. in CI).
//
// The tolerance accounts for legitimate refill: between the first and last
// of n requests hitting the server, a small amount of real time passes, and
// a client configured with RPS > 0 will have refilled a few extra tokens
// (or slid its window) in that window, so allowed can be a little more than
// the raw burst capacity without indicating a bug.
func runCorrectness(n, expect, allowed, denied, errs int64, elapsed time.Duration) {
	fmt.Printf("Mode: correctness\n")
	fmt.Printf("Total requests: %d\n", n)
	fmt.Printf("Allowed:        %d\n", allowed)
	fmt.Printf("Denied:         %d\n", denied)
	fmt.Printf("Errors:         %d\n", errs)
	fmt.Printf("Elapsed:        %v\n", elapsed)

	if expect <= 0 {
		return
	}

	const tolerance = 5
	low, high := expect, expect+tolerance
	if allowed >= low && allowed <= high {
		fmt.Printf("PASS: allowed=%d is within expected range [%d, %d]\n", allowed, low, high)
		return
	}

	fmt.Printf("FAIL: allowed=%d, want in range [%d, %d]\n", allowed, low, high)
	os.Exit(1)
}

// runThroughput prints achieved requests/sec plus latency percentiles and
// min/max, computed from every request's latency merged into one slice.
func runThroughput(n, allowed, denied, errs int64, latencies []time.Duration, elapsed time.Duration) {
	fmt.Printf("Mode: throughput\n")
	fmt.Printf("Total requests: %d\n", n)
	fmt.Printf("Allowed:        %d (informational)\n", allowed)
	fmt.Printf("Denied:         %d (informational)\n", denied)
	fmt.Printf("Errors:         %d\n", errs)
	fmt.Printf("Elapsed:        %v\n", elapsed)

	throughput := float64(n) / elapsed.Seconds()
	fmt.Printf("Throughput:     %.1f req/sec\n", throughput)

	if len(latencies) == 0 {
		fmt.Println("No successful requests to compute latency percentiles from.")
		return
	}

	// Percentiles need the latencies in sorted order; everything below
	// (percentile, min, max) just indexes into this one sorted slice.
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	fmt.Printf("Latency min:    %v\n", latencies[0])
	fmt.Printf("Latency p50:    %v\n", percentile(latencies, 0.50))
	fmt.Printf("Latency p95:    %v\n", percentile(latencies, 0.95))
	fmt.Printf("Latency p99:    %v\n", percentile(latencies, 0.99))
	fmt.Printf("Latency max:    %v\n", latencies[len(latencies)-1])
}

// percentile returns the p-th percentile (0 < p <= 1) of an already-sorted
// slice of latencies, using the nearest-rank method: the index is
// ceil(p*len)-1, clamped into the valid range. E.g. for p=0.95 and 100
// samples, index = ceil(95)-1 = 94, the 95th-smallest value (1-indexed).
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
