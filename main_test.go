package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDeleteFromCollection(t *testing.T) {
	// Create temporary store
	dbPath := t.TempDir() + "/test.db"
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	srv := &Server{store: store}
	handler := srv.routes()

	// 1. Create a record to delete
	err = store.Put("testcol", "key1", []byte(`"val1"`))
	if err != nil {
		t.Fatalf("failed to put: %v", err)
	}

	t.Run("DELETE via query param", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/collections/testcol?key=key1", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("expected StatusNoContent, got %d, body: %s", rec.Code, rec.Body.String())
		}

		// Verify it was deleted
		_, err := store.Get("testcol", "key1")
		if err != ErrNotFound {
			t.Errorf("expected key to be deleted, got err: %v", err)
		}
	})

	// 2. Put it back and delete via body
	err = store.Put("testcol", "key1", []byte(`"val1"`))
	if err != nil {
		t.Fatalf("failed to put: %v", err)
	}

	t.Run("DELETE via body", func(t *testing.T) {
		body := strings.NewReader(`{"key": "key1"}`)
		req := httptest.NewRequest(http.MethodDelete, "/collections/testcol", body)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("expected StatusNoContent, got %d, body: %s", rec.Code, rec.Body.String())
		}

		// Verify it was deleted
		_, err := store.Get("testcol", "key1")
		if err != ErrNotFound {
			t.Errorf("expected key to be deleted, got err: %v", err)
		}
	})

	t.Run("DELETE missing key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/collections/testcol?key=nonexistent", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected StatusNotFound, got %d, body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("DELETE no key specified", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/collections/testcol", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected StatusBadRequest, got %d, body: %s", rec.Code, rec.Body.String())
		}
	})
}
