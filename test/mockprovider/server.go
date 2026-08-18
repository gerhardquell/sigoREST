//**********************************************************************
//      test/mockprovider/server.go
//**********************************************************************
//  Beschreibung: Mock-Provider für Rate-Limit-Tests.
//  Simuliert echtes Provider-Limit: zählt Requests pro 1s-Fenster,
//  schickt HTTP 429 sobald threshold überschritten.
//  OpenAI-kompatibel: POST /v1/chat/completions, GET /v1/models.
//**********************************************************************

package mockprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// MockProvider simuliert einen Provider mit Rate-Limit (Fixed-Window 1s).
type MockProvider struct {
	mu        sync.Mutex
	threshold int           // max Requests pro 1s-Fenster
	window    time.Duration // Fenster-Größe
	count     int           // Requests im aktuellen Fenster
	windowEnd time.Time     // Ende des aktuellen Fensters
	server    *httptest.Server
	totalReqs int64
	rejected  int64
}

// New erzeugt einen MockProvider mit gegebener Schwellrate (Requests/Sekunde).
// Liefert einen laufenden httptest-Server (für automatisierte Tests).
func New(requestsPerSecond int) *MockProvider {
	mp := &MockProvider{
		threshold: requestsPerSecond,
		window:    time.Second,
	}
	mp.server = httptest.NewServer(http.HandlerFunc(mp.handle))
	return mp
}

// NewStandalone liefert den Mock-Handler als http.Handler ohne eigenen Server.
// Für standalone-Betrieb (eigener http.Server, gewählter Port).
func NewStandalone(requestsPerSecond int) http.Handler {
	mp := &MockProvider{
		threshold: requestsPerSecond,
		window:    time.Second,
	}
	return http.HandlerFunc(mp.handle)
}

// URL liefert die Basis-URL des Mock-Servers.
func (mp *MockProvider) URL() string { return mp.server.URL }

// Close stoppt den Mock-Server.
func (mp *MockProvider) Close() { mp.server.Close() }

// Stats liefert Gesamt-Requests und abgewiesene (429) Requests.
type Stats struct {
	Total    int64
	Rejected int64
}

func (mp *MockProvider) Stats() Stats {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	return Stats{Total: mp.totalReqs, Rejected: mp.rejected}
}

// allow prüft Rate-Limit: true=erlaubt, false=429.
func (mp *MockProvider) allow() bool {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	now := time.Now()
	if now.After(mp.windowEnd) {
		mp.count = 0
		mp.windowEnd = now.Add(mp.window)
	}
	mp.totalReqs++
	if mp.count >= mp.threshold {
		mp.rejected++
		return false
	}
	mp.count++
	return true
}

func (mp *MockProvider) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/models":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"data": []map[string]interface{}{{
				"id": "mock-model", "object": "model", "owned_by": "mock",
			}},
		})
		return

	case "/v1/chat/completions":
		if !mp.allow() {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"type":    "rate_limit_exceeded",
					"message": "Mock rate limit reached",
				},
			})
			return
		}
		// Echo-Antwort (OpenAI-kompatibel)
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "mock-1",
			"object":  "chat.completion",
			"model":   "mock-model",
			"choices": []map[string]interface{}{{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": "mock-ok"}, "finish_reason": "stop"}},
		})
		return

	default:
		http.NotFound(w, r)
	}
}
