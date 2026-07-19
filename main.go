package main

import (
	"log"
	"net/http"
)

// healthHandler responds to health-check requests with a plain-text "OK".
// It's used to prove the server is up and reachable.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func main() {
	// The address the server binds to (all interfaces, port 8080).
	const addr = ":8080"

	// Register the single route: GET /health -> healthHandler.
	// The "GET /health" pattern (Go 1.22+) matches only GET requests.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)

	// Announce that the server is starting and where it's listening.
	log.Printf("server listening on %s", addr)

	// Start the server; ListenAndServe blocks until the server stops.
	// If it returns an error (e.g. port already in use), log it and exit.
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
