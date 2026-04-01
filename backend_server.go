// backend_server.go
// Run this file multiple times with different ports to simulate real backend servers.
//
// Usage:
//   go run backend_server.go 9001
//   go run backend_server.go 9002
//   go run backend_server.go 9003

package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	port := "9001"
	if len(os.Args) > 1 {
		port = os.Args[1]
	}

	// /health — the load balancer pings this
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	})

	// / — respond with server info for every request
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Simulate a small delay so you can see load distribution
		time.Sleep(50 * time.Millisecond)

		log.Printf("[Server :%s] Got request: %s %s", port, r.Method, r.URL.Path)

		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "👋 Hello from Backend Server on port %s!\n", port)
		fmt.Fprintf(w, "Path: %s\n", r.URL.Path)
		fmt.Fprintf(w, "Time: %s\n", time.Now().Format(time.RFC3339))
	})

	addr := ":" + port
	fmt.Printf("🟢 Backend server running on http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
