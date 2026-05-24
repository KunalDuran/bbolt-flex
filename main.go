package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

const dbFile = "app.db"

// collectionRe restricts collection names to safe characters so people can't
// smuggle slashes or weird bytes into bucket names via the URL.
var collectionRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var (
	ErrNotFound       = errors.New("not found")
	ErrBadCollection  = errors.New("invalid collection name")
	ErrBadKey         = errors.New("invalid key")
	ErrCollectionGone = errors.New("collection does not exist")
)

// Store is a thin wrapper over bbolt that treats every bucket as a generic
// JSON document collection. Values are stored as raw JSON bytes so callers can
// PUT any shape they want.
type Store struct{ db *bolt.DB }

func NewStore(path string) (*Store, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open bbolt: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Put inserts or updates a key inside a collection. The bucket is created on
// demand so the first POST to a new collection just works.
func (s *Store) Put(collection, key string, value json.RawMessage) error {
	if !collectionRe.MatchString(collection) {
		return ErrBadCollection
	}
	if strings.TrimSpace(key) == "" {
		return ErrBadKey
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(collection))
		if err != nil {
			return err
		}
		return b.Put([]byte(key), value)
	})
}

// Update requires the key to already exist; this is what lets PUT distinguish
// "real update" from "upsert" if a caller cares about that.
func (s *Store) Update(collection, key string, value json.RawMessage) error {
	if !collectionRe.MatchString(collection) {
		return ErrBadCollection
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(collection))
		if b == nil {
			return ErrCollectionGone
		}
		if b.Get([]byte(key)) == nil {
			return ErrNotFound
		}
		return b.Put([]byte(key), value)
	})
}

func (s *Store) Get(collection, key string) (json.RawMessage, error) {
	if !collectionRe.MatchString(collection) {
		return nil, ErrBadCollection
	}
	var out json.RawMessage
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(collection))
		if b == nil {
			return ErrCollectionGone
		}
		v := b.Get([]byte(key))
		if v == nil {
			return ErrNotFound
		}
		// Copy: bbolt's returned slice is only valid inside the tx.
		out = append(json.RawMessage{}, v...)
		return nil
	})
	return out, err
}

// All returns every key/value pair in the collection as a map. For large
// collections you'd want pagination via Cursor + a ?after= query param, but
// keeping it simple here.
func (s *Store) All(collection string) (map[string]json.RawMessage, error) {
	if !collectionRe.MatchString(collection) {
		return nil, ErrBadCollection
	}
	out := map[string]json.RawMessage{}
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(collection))
		if b == nil {
			return ErrCollectionGone
		}
		return b.ForEach(func(k, v []byte) error {
			out[string(k)] = append(json.RawMessage{}, v...)
			return nil
		})
	})
	return out, err
}

func (s *Store) Delete(collection, key string) error {
	if !collectionRe.MatchString(collection) {
		return ErrBadCollection
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(collection))
		if b == nil {
			return ErrCollectionGone
		}
		if b.Get([]byte(key)) == nil {
			return ErrNotFound
		}
		return b.Delete([]byte(key))
	})
}

// Collections lists every bucket so callers can discover what's stored.
func (s *Store) Collections() ([]string, error) {
	var names []string
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
			names = append(names, string(name))
			return nil
		})
	})
	return names, err
}

// ---- HTTP layer ----

type Server struct{ store *Store }

// Routes:
//   GET    /collections                       -> list collection names
//   GET    /collections/{c}                   -> all records in {c}
//   POST   /collections/{c}                   -> body: {"key":"...", "value": <any json>}
//   GET    /collections/{c}/{key}             -> single record
//   PUT    /collections/{c}/{key}             -> replace; body is the value (any json)
//   DELETE /collections/{c}/{key}             -> delete
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/collections", s.handleCollectionsRoot)
	mux.HandleFunc("/collections/", s.handleCollections)
	return mux
}

func (s *Server) handleCollectionsRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	names, err := s.store.Collections()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, names)
}

func (s *Server) handleCollections(w http.ResponseWriter, r *http.Request) {
	// Path is /collections/{collection}[/{key}]
	rest := strings.TrimPrefix(r.URL.Path, "/collections/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		writeError(w, http.StatusBadRequest, "collection name required")
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	collection := parts[0]
	if !collectionRe.MatchString(collection) {
		writeError(w, http.StatusBadRequest, "invalid collection name (allowed: A-Z a-z 0-9 _ -, up to 64 chars)")
		return
	}

	if len(parts) == 1 {
		s.handleCollection(w, r, collection)
		return
	}
	s.handleItem(w, r, collection, parts[1])
}

// /collections/{c}
func (s *Server) handleCollection(w http.ResponseWriter, r *http.Request, collection string) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.All(collection)
		if errors.Is(err, ErrCollectionGone) {
			// An empty result is friendlier than 404 here; clients can
			// treat "no such collection" and "empty collection" the same.
			writeJSON(w, http.StatusOK, map[string]json.RawMessage{})
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, items)

	case http.MethodPost:
		// Body shape: {"key": "...", "value": <any json>}
		var body struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
			return
		}
		if strings.TrimSpace(body.Key) == "" {
			writeError(w, http.StatusBadRequest, "'key' is required")
			return
		}
		if len(body.Value) == 0 {
			writeError(w, http.StatusBadRequest, "'value' is required")
			return
		}
		if err := s.store.Put(collection, body.Key, body.Value); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"collection": collection,
			"key":        body.Key,
			"value":      body.Value,
		})

	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// /collections/{c}/{key}
func (s *Server) handleItem(w http.ResponseWriter, r *http.Request, collection, key string) {
	if strings.Contains(key, "/") || key == "" {
		writeError(w, http.StatusBadRequest, "invalid key")
		return
	}

	switch r.Method {
	case http.MethodGet:
		v, err := s.store.Get(collection, key)
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrCollectionGone) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(v)

	case http.MethodPut:
		// The whole body is the new value (any JSON). This matches the way
		// most KV-style APIs work and avoids nesting under a "value" field.
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
			return
		}
		if len(raw) == 0 {
			writeError(w, http.StatusBadRequest, "body required")
			return
		}
		// Upsert semantics: create the collection/key if missing. If you want
		// strict update-only, swap Put -> Update here.
		if err := s.store.Put(collection, key, raw); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"collection": collection,
			"key":        key,
			"value":      raw,
		})

	case http.MethodDelete:
		err := s.store.Delete(collection, key)
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrCollectionGone) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func main() {
	store, err := NewStore(dbFile)
	if err != nil {
		log.Fatalf("store init: %v", err)
	}
	defer store.Close()

	srv := &Server{store: store}
	addr := ":8081"
	log.Printf("listening on %s (db=%s)", addr, dbFile)
	if err := http.ListenAndServe(addr, corsMiddleware(srv.routes())); err != nil {
		log.Fatalf("server: %v", err)
	}
}
