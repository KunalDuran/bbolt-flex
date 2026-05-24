package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSMiddleware(t *testing.T) {
	// Create a dummy handler that returns 200 OK
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wrap it with corsMiddleware
	handler := corsMiddleware(dummyHandler)

	t.Run("Preflight Request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/collections", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		res := rec.Result()
		defer res.Body.Close()

		if res.StatusCode != http.StatusNoContent {
			t.Errorf("expected status 204, got %d", res.StatusCode)
		}

		if origin := res.Header.Get("Access-Control-Allow-Origin"); origin != "*" {
			t.Errorf("expected Access-Control-Allow-Origin to be '*', got %q", origin)
		}

		if methods := res.Header.Get("Access-Control-Allow-Methods"); methods != "GET, POST, PUT, DELETE, OPTIONS" {
			t.Errorf("unexpected Access-Control-Allow-Methods: %q", methods)
		}

		if headers := res.Header.Get("Access-Control-Allow-Headers"); headers != "Content-Type, Authorization" {
			t.Errorf("unexpected Access-Control-Allow-Headers: %q", headers)
		}

		if maxAge := res.Header.Get("Access-Control-Max-Age"); maxAge != "86400" {
			t.Errorf("unexpected Access-Control-Max-Age: %q", maxAge)
		}
	})

	t.Run("Actual Request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/collections", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		res := rec.Result()
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", res.StatusCode)
		}

		if origin := res.Header.Get("Access-Control-Allow-Origin"); origin != "*" {
			t.Errorf("expected Access-Control-Allow-Origin to be '*', got %q", origin)
		}
	})
}
