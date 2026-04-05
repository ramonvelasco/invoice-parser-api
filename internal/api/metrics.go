package api

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics provides Prometheus-compatible metrics collection.
type Metrics struct {
	mu             sync.RWMutex
	requestCounts  map[string]*atomic.Int64 // "endpoint:status" -> count
	requestLatency map[string]*latencyBucket // "endpoint" -> latency stats
	startTime      time.Time
}

type latencyBucket struct {
	mu    sync.Mutex
	count int64
	sum   float64 // total milliseconds
	max   float64
}

func NewMetrics() *Metrics {
	return &Metrics{
		requestCounts:  make(map[string]*atomic.Int64),
		requestLatency: make(map[string]*latencyBucket),
		startTime:      time.Now(),
	}
}

func (m *Metrics) RecordRequest(endpoint string, status int, duration time.Duration) {
	// Count by endpoint+status
	key := fmt.Sprintf("%s:%d", endpoint, status)
	m.mu.RLock()
	counter, ok := m.requestCounts[key]
	m.mu.RUnlock()

	if !ok {
		m.mu.Lock()
		counter, ok = m.requestCounts[key]
		if !ok {
			counter = &atomic.Int64{}
			m.requestCounts[key] = counter
		}
		m.mu.Unlock()
	}
	counter.Add(1)

	// Latency by endpoint
	m.mu.RLock()
	bucket, ok := m.requestLatency[endpoint]
	m.mu.RUnlock()

	if !ok {
		m.mu.Lock()
		bucket, ok = m.requestLatency[endpoint]
		if !ok {
			bucket = &latencyBucket{}
			m.requestLatency[endpoint] = bucket
		}
		m.mu.Unlock()
	}

	ms := float64(duration.Milliseconds())
	bucket.mu.Lock()
	bucket.count++
	bucket.sum += ms
	if ms > bucket.max {
		bucket.max = ms
	}
	bucket.mu.Unlock()
}

// Handler returns Prometheus-compatible /metrics output in text exposition format.
func (m *Metrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		// Uptime
		uptime := time.Since(m.startTime).Seconds()
		fmt.Fprintf(w, "# HELP invoiceparser_uptime_seconds Time since server start.\n")
		fmt.Fprintf(w, "# TYPE invoiceparser_uptime_seconds gauge\n")
		fmt.Fprintf(w, "invoiceparser_uptime_seconds %.2f\n\n", uptime)

		// Request counts
		fmt.Fprintf(w, "# HELP invoiceparser_requests_total Total number of requests.\n")
		fmt.Fprintf(w, "# TYPE invoiceparser_requests_total counter\n")

		m.mu.RLock()
		keys := make([]string, 0, len(m.requestCounts))
		for k := range m.requestCounts {
			keys = append(keys, k)
		}
		m.mu.RUnlock()
		sort.Strings(keys)

		for _, key := range keys {
			m.mu.RLock()
			counter := m.requestCounts[key]
			m.mu.RUnlock()

			// Parse endpoint:status
			var endpoint, status string
			for i := len(key) - 1; i >= 0; i-- {
				if key[i] == ':' {
					endpoint = key[:i]
					status = key[i+1:]
					break
				}
			}
			fmt.Fprintf(w, "invoiceparser_requests_total{endpoint=%q,status=%q} %d\n", endpoint, status, counter.Load())
		}

		// Latency
		fmt.Fprintf(w, "\n# HELP invoiceparser_request_duration_ms Request latency in milliseconds.\n")
		fmt.Fprintf(w, "# TYPE invoiceparser_request_duration_ms summary\n")

		m.mu.RLock()
		endpoints := make([]string, 0, len(m.requestLatency))
		for k := range m.requestLatency {
			endpoints = append(endpoints, k)
		}
		m.mu.RUnlock()
		sort.Strings(endpoints)

		for _, ep := range endpoints {
			m.mu.RLock()
			bucket := m.requestLatency[ep]
			m.mu.RUnlock()

			bucket.mu.Lock()
			count := bucket.count
			sum := bucket.sum
			max := bucket.max
			var avg float64
			if count > 0 {
				avg = sum / float64(count)
			}
			bucket.mu.Unlock()

			fmt.Fprintf(w, "invoiceparser_request_duration_ms_count{endpoint=%q} %d\n", ep, count)
			fmt.Fprintf(w, "invoiceparser_request_duration_ms_sum{endpoint=%q} %.2f\n", ep, sum)
			fmt.Fprintf(w, "invoiceparser_request_duration_ms_avg{endpoint=%q} %.2f\n", ep, avg)
			fmt.Fprintf(w, "invoiceparser_request_duration_ms_max{endpoint=%q} %.2f\n", ep, max)
		}
	}
}
