package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHomeHandler_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	homeHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body["version"] == "" {
		t.Fatal("expected version in response")
	}
}

func TestHomeHandler_404ForUnknownPath(t *testing.T) {
	// "/" must not act as a catch-all anymore.
	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	rec := httptest.NewRecorder()
	homeHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHomeHandler_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	homeHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHealthHandler_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	healthHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body["status"] != "healthy" {
		t.Fatalf("expected healthy, got %q", body["status"])
	}
}

func TestLiveReadyHandlers(t *testing.T) {
	for path, h := range map[string]http.HandlerFunc{
		"/live":  liveHandler,
		"/ready": readyHandler,
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, rec.Code)
		}
	}
}

func TestInstrument_RecordsActualStatus(t *testing.T) {
	before := testutil.ToFloat64(requestCount.WithLabelValues("/test-404", "404"))

	h := instrument("/test-404", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "nope"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test-404", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	after := testutil.ToFloat64(requestCount.WithLabelValues("/test-404", "404"))
	if after-before != 1 {
		t.Fatalf("expected 404 counter to increment by 1, got %v -> %v", before, after)
	}

	// The old bug recorded everything as 200; guard against regression.
	if got := testutil.ToFloat64(requestCount.WithLabelValues("/test-404", "200")); got != 0 {
		t.Fatalf("expected no 200 label for a 404 response, got %v", got)
	}
}

func TestInstrument_RecoversPanicAs500(t *testing.T) {
	h := instrument("/test-panic", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	req := httptest.NewRequest(http.MethodGet, "/test-panic", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 after panic, got %d", rec.Code)
	}
}

func TestGetEnv(t *testing.T) {
	t.Setenv("GUARDRAIL_TEST_KEY", "x")
	if getEnv("GUARDRAIL_TEST_KEY", "fallback") != "x" {
		t.Fatal("expected env value")
	}
	if getEnv("GUARDRAIL_TEST_KEY_MISSING", "fallback") != "fallback" {
		t.Fatal("expected fallback")
	}
}
