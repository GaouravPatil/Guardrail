package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var Version = getEnv("APP_VERSION", "v2")

// --- Metrics definitions ---

var (
	requestCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "guardrail_requests_total",
			Help: "Total number of HTTP requests, labeled by path and status",
		},
		[]string{"path", "status"},
	)

	requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "guardrail_request_duration_seconds",
			Help:    "Request latency in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path"},
	)
)

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// instrument wraps any handler to automatically record count + latency,
func instrument(path string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		handler(w, r)
		duration := time.Since(start).Seconds()

		requestCount.WithLabelValues(path, "200").Inc()
		requestDuration.WithLabelValues(path).Observe(duration)
	}
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	resp := map[string]string{
		"message": "Hello from the Guardrail",
		"version": Version,
	}
	json.NewEncoder(w).Encode(resp)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	resp := map[string]string{
		"status":  "healthy",
		"version": Version,
	}
	json.NewEncoder(w).Encode(resp)
}

func main() {
	http.HandleFunc("/", instrument("/", homeHandler))
	http.HandleFunc("/health", instrument("/health", healthHandler))

	// This is the endpoint Prometheus will scrape — provided automatically
	// by the client library, no manual work needed
	http.Handle("/metrics", promhttp.Handler())

	log.Println("Server starting on :5000, version:", Version)
	log.Fatal(http.ListenAndServe(":5000", nil))
}
