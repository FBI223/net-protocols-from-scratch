package main

import (
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/FBI223/net-protocols-from-scratch/http1"
)

// store is an in-memory key-value store of JSON documents.
type store struct {
	mu   sync.RWMutex
	data map[string]entry
}

type entry struct {
	value   []byte
	version uint64
	updated time.Time
}

func newStore() *store { return &store{data: make(map[string]entry)} }

// httpDate is the IMF-fixdate format of RFC 9110 §5.6.7.
const httpDate = "Mon, 02 Jan 2006 15:04:05 GMT"

const (
	maxKeyLen = 128
	maxKeys   = 10000
)

var errStoreFull = errors.New("store is full")

// validKey allows letters, digits and "-_.". Keys come from URLs, so keeping
// them plain avoids any question about escaping later.
func validKey(k string) bool {
	if k == "" || len(k) > maxKeyLen || k == "." || k == ".." {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '.'
		if !ok {
			return false
		}
	}
	return true
}

func (s *store) get(k string) (entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.data[k]
	return e, ok
}

// put stores v. With onlyIfAbsent it fails (created=false, stored=false)
// when the key exists.
func (s *store) put(k string, v []byte, onlyIfAbsent bool) (e entry, created, stored bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.data[k]
	if exists && onlyIfAbsent {
		return old, false, false, nil
	}
	if !exists && len(s.data) >= maxKeys {
		return entry{}, false, false, errStoreFull
	}
	e = entry{value: v, version: old.version + 1, updated: time.Now()}
	s.data[k] = e
	return e, !exists, true, nil
}

func (s *store) delete(k string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data[k]
	delete(s.data, k)
	return ok
}

func (s *store) keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mountKV registers the key-value API:
//
//	GET    /kv          list of keys
//	GET    /kv/{key}    the stored JSON document
//	PUT    /kv/{key}    store a JSON document (201 new, 200 replaced);
//	                    "If-None-Match: *" makes it create-only (412 if present)
//	DELETE /kv/{key}    remove (204, or 404)
func mountKV(rt *http1.Router, s *store) {
	rt.HandleFunc("GET", "/kv", func(w *http1.ResponseWriter, r *http1.Request) {
		writeJSON(w, 200, map[string]any{"keys": s.keys()})
	})

	rt.HandleFunc("GET", "/kv/{key}", func(w *http1.ResponseWriter, r *http1.Request) {
		key := r.PathValue("key")
		if !validKey(key) {
			http1.Error(w, 400, "invalid key")
			return
		}
		e, ok := s.get(key)
		if !ok {
			http1.Error(w, 404, "no such key")
			return
		}
		setEntryHeaders(w, e)
		w.Header().Set("Content-Type", "application/json")
		w.Write(e.value)
	})

	rt.HandleFunc("PUT", "/kv/{key}", func(w *http1.ResponseWriter, r *http1.Request) {
		key := r.PathValue("key")
		if !validKey(key) {
			http1.Error(w, 400, "invalid key")
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			status, ok := http1.StatusCode(err)
			if !ok {
				status = 400
			}
			http1.Error(w, status, http1.StatusText(status))
			return
		}
		if !json.Valid(body) {
			http1.Error(w, 400, "body is not valid JSON")
			return
		}
		onlyIfAbsent := r.Header.Get("If-None-Match") == "*"
		e, created, stored, err := s.put(key, body, onlyIfAbsent)
		switch {
		case errors.Is(err, errStoreFull):
			http1.Error(w, 507, "store is full")
		case err != nil:
			http1.Error(w, 500, "internal error")
		case !stored:
			setEntryHeaders(w, e)
			http1.Error(w, 412, "key already exists")
		default:
			setEntryHeaders(w, e)
			status := 200
			if created {
				status = 201
				w.Header().Set("Location", "/kv/"+key)
			}
			writeJSON(w, status, map[string]any{"key": key, "version": e.version})
		}
	})

	rt.HandleFunc("DELETE", "/kv/{key}", func(w *http1.ResponseWriter, r *http1.Request) {
		key := r.PathValue("key")
		if !validKey(key) {
			http1.Error(w, 400, "invalid key")
			return
		}
		if !s.delete(key) {
			http1.Error(w, 404, "no such key")
			return
		}
		w.WriteHeader(204)
	})
}

func setEntryHeaders(w *http1.ResponseWriter, e entry) {
	w.Header().Set("ETag", `"`+strconv.FormatUint(e.version, 10)+`"`)
	w.Header().Set("Last-Modified", e.updated.UTC().Format(httpDate))
}

func writeJSON(w *http1.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http1.Error(w, 500, "internal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(append(b, '\n'))
}
