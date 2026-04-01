package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// ==============================
// 1. CONFIGURATION
// ==============================

// Config holds all settings loaded from config.json
type Config struct {
	Port            string          `json:"port"`
	HealthCheckPath string          `json:"health_check_path"`
	HealthInterval  int             `json:"health_check_interval_seconds"`
	Algorithm       string          `json:"algorithm"` // "round_robin" or "least_connections"
	Backends        []BackendConfig `json:"backends"`
}

// BackendConfig is one server entry in config.json
type BackendConfig struct {
	URL    string `json:"url"`
	Weight int    `json:"weight"` // used for weighted round robin
}

// ==============================
// 2. BACKEND SERVER
// ==============================

// Backend represents a single backend server
type Backend struct {
	URL          *url.URL
	Weight       int
	IsHealthy    bool
	ActiveConns  int64  // number of active connections right now
	TotalReqs    int64  // total requests served (for stats)
	mu           sync.RWMutex
	ReverseProxy *httputil.ReverseProxy
}

// SetHealthy marks a backend as healthy or unhealthy (thread-safe)
func (b *Backend) SetHealthy(healthy bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.IsHealthy = healthy
}

// GetHealthy reads the health status (thread-safe)
func (b *Backend) GetHealthy() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.IsHealthy
}

// ==============================
// 3. LOAD BALANCER
// ==============================

// LoadBalancer holds all backends and distributes requests
type LoadBalancer struct {
	backends  []*Backend
	current   uint64 // atomic counter for round robin
	algorithm string
	mu        sync.RWMutex

	// Stats
	totalRequests int64
	totalErrors   int64
	startTime     time.Time
}

// NewLoadBalancer creates a new load balancer from config
func NewLoadBalancer(cfg Config) *LoadBalancer {
	lb := &LoadBalancer{
		algorithm: cfg.Algorithm,
		startTime: time.Now(),
	}

	for _, bcfg := range cfg.Backends {
		parsedURL, err := url.Parse(bcfg.URL)
		if err != nil {
			log.Fatalf("❌ Invalid backend URL %s: %v", bcfg.URL, err)
		}

		weight := bcfg.Weight
		if weight <= 0 {
			weight = 1
		}

		// Create the reverse proxy for this backend
		proxy := httputil.NewSingleHostReverseProxy(parsedURL)

		backend := &Backend{
			URL:          parsedURL,
			Weight:       weight,
			IsHealthy:    true, // assume healthy at start
			ReverseProxy: proxy,
		}

		lb.backends = append(lb.backends, backend)
		log.Printf("✅ Registered backend: %s (weight: %d)", parsedURL, weight)
	}

	return lb
}

// getNextRoundRobin picks the next healthy backend using round robin
func (lb *LoadBalancer) getNextRoundRobin() *Backend {
	totalBackends := uint64(len(lb.backends))
	if totalBackends == 0 {
		return nil
	}

	// Try each backend once; skip unhealthy ones
	for i := uint64(0); i < totalBackends; i++ {
		next := atomic.AddUint64(&lb.current, 1)
		idx := (next - 1) % totalBackends
		backend := lb.backends[idx]
		if backend.GetHealthy() {
			return backend
		}
	}

	return nil // all backends are down
}

// getWeightedRoundRobin picks a backend based on weights
func (lb *LoadBalancer) getWeightedRoundRobin() *Backend {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	// Build a list where each backend appears Weight times
	var pool []*Backend
	for _, b := range lb.backends {
		if b.GetHealthy() {
			for i := 0; i < b.Weight; i++ {
				pool = append(pool, b)
			}
		}
	}

	if len(pool) == 0 {
		return nil
	}

	next := atomic.AddUint64(&lb.current, 1)
	return pool[(next-1)%uint64(len(pool))]
}

// getLeastConnections picks the backend with fewest active connections
func (lb *LoadBalancer) getLeastConnections() *Backend {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	var best *Backend
	for _, b := range lb.backends {
		if !b.GetHealthy() {
			continue
		}
		if best == nil || atomic.LoadInt64(&b.ActiveConns) < atomic.LoadInt64(&best.ActiveConns) {
			best = b
		}
	}
	return best
}

// NextBackend picks the next backend based on the chosen algorithm
func (lb *LoadBalancer) NextBackend() *Backend {
	switch lb.algorithm {
	case "least_connections":
		return lb.getLeastConnections()
	case "weighted_round_robin":
		return lb.getWeightedRoundRobin()
	default: // "round_robin"
		return lb.getNextRoundRobin()
	}
}

// ==============================
// 4. HTTP HANDLER
// ==============================

// ServeHTTP is called for every incoming request
func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&lb.totalRequests, 1)

	backend := lb.NextBackend()
	if backend == nil {
		atomic.AddInt64(&lb.totalErrors, 1)
		http.Error(w, "⚠️ No healthy backends available. Please try again later.", http.StatusServiceUnavailable)
		log.Printf("❌ No healthy backends for request: %s %s", r.Method, r.URL.Path)
		return
	}

	// Track active connections
	atomic.AddInt64(&backend.ActiveConns, 1)
	defer atomic.AddInt64(&backend.ActiveConns, -1)

	// Count total requests to this backend
	atomic.AddInt64(&backend.TotalReqs, 1)

	log.Printf("➡️  [%s] %s %s → %s", lb.algorithm, r.Method, r.URL.Path, backend.URL)

	// Forward the request to the chosen backend
	backend.ReverseProxy.ServeHTTP(w, r)
}

// ==============================
// 5. HEALTH CHECKER
// ==============================

// StartHealthChecks runs periodic health checks in the background
func (lb *LoadBalancer) StartHealthChecks(path string, intervalSeconds int) {
	interval := time.Duration(intervalSeconds) * time.Second
	log.Printf("🏥 Health checks every %v on path: %s", interval, path)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			lb.checkAllBackends(path)
		}
	}()
}

// checkAllBackends pings every backend once
func (lb *LoadBalancer) checkAllBackends(path string) {
	for _, backend := range lb.backends {
		go lb.checkBackend(backend, path)
	}
}

// checkBackend pings one backend and updates its health
func (lb *LoadBalancer) checkBackend(b *Backend, path string) {
	checkURL := fmt.Sprintf("%s%s", b.URL, path)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(checkURL)

	wasHealthy := b.GetHealthy()

	if err != nil || resp.StatusCode != http.StatusOK {
		b.SetHealthy(false)
		if wasHealthy {
			log.Printf("🔴 Backend DOWN: %s (error: %v)", b.URL, err)
		}
		return
	}
	defer resp.Body.Close()

	b.SetHealthy(true)
	if !wasHealthy {
		log.Printf("🟢 Backend RECOVERED: %s", b.URL)
	}
}

// ==============================
// 6. STATS ENDPOINT
// ==============================

// StatsHandler shows a simple JSON stats page at /lb-stats
func (lb *LoadBalancer) StatsHandler(w http.ResponseWriter, r *http.Request) {
	type BackendStat struct {
		URL         string `json:"url"`
		Healthy     bool   `json:"healthy"`
		ActiveConns int64  `json:"active_connections"`
		TotalReqs   int64  `json:"total_requests"`
		Weight      int    `json:"weight"`
	}

	type Stats struct {
		Uptime        string        `json:"uptime"`
		Algorithm     string        `json:"algorithm"`
		TotalRequests int64         `json:"total_requests"`
		TotalErrors   int64         `json:"total_errors"`
		Backends      []BackendStat `json:"backends"`
	}

	stats := Stats{
		Uptime:        time.Since(lb.startTime).Round(time.Second).String(),
		Algorithm:     lb.algorithm,
		TotalRequests: atomic.LoadInt64(&lb.totalRequests),
		TotalErrors:   atomic.LoadInt64(&lb.totalErrors),
	}

	for _, b := range lb.backends {
		stats.Backends = append(stats.Backends, BackendStat{
			URL:         b.URL.String(),
			Healthy:     b.GetHealthy(),
			ActiveConns: atomic.LoadInt64(&b.ActiveConns),
			TotalReqs:   atomic.LoadInt64(&b.TotalReqs),
			Weight:      b.Weight,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// ==============================
// 7. MAIN
// ==============================

func main() {
	// Load config file
	cfgFile, err := os.ReadFile("config.json")
	if err != nil {
		log.Fatal("❌ Could not read config.json: ", err)
	}

	var cfg Config
	if err := json.Unmarshal(cfgFile, &cfg); err != nil {
		log.Fatal("❌ Could not parse config.json: ", err)
	}

	// Default values
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.HealthCheckPath == "" {
		cfg.HealthCheckPath = "/health"
	}
	if cfg.HealthInterval <= 0 {
		cfg.HealthInterval = 10
	}
	if cfg.Algorithm == "" {
		cfg.Algorithm = "round_robin"
	}

	// Create load balancer
	lb := NewLoadBalancer(cfg)

	// Start health checks in background
	lb.StartHealthChecks(cfg.HealthCheckPath, cfg.HealthInterval)

	// Set up HTTP routes
	mux := http.NewServeMux()
	mux.HandleFunc("/lb-stats", lb.StatsHandler) // stats page
	mux.Handle("/", lb)                           // everything else goes through LB

	addr := ":" + cfg.Port
	fmt.Printf("\n🚀 Load Balancer started on http://localhost%s\n", addr)
	fmt.Printf("📊 Stats available at http://localhost%s/lb-stats\n", addr)
	fmt.Printf("⚙️  Algorithm: %s\n\n", cfg.Algorithm)

	log.Fatal(http.ListenAndServe(addr, mux))
}
