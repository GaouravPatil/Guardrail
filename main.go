package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

var Version = getEnv("APP_VERSION", "v2")

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	resp := map[string]string{
		"message": "Hello from the Guardrail",
		"version": Version,
	}
	json.NewEncoder(w).Encode(resp)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	// Kept cheap and dependency-free — this is what Kubernetes will poll constantly
	w.WriteHeader(http.StatusOK)
	resp := map[string]string{
		"status":  "healthy",
		"version": Version,
	}
	json.NewEncoder(w).Encode(resp)
}

func main() {
	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/health", healthHandler)

	log.Println("Server starting on :5000, version:", Version)
	log.Fatal(http.ListenAndServe(":5000", nil))
}
