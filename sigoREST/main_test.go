package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigorest/sigoengine"
)

func newTestServer(t *testing.T) (*Server, string) {
	dir := t.TempDir()

	registry := sigoengine.NewChannelRegistry(filepath.Join(dir, "channels.json"))
	registry.AddChannel(&sigoengine.Channel{
		Provider: "mammouth",
		Name:     "default",
		APIKey:   "default-key",
		Active:   true,
		Order:    0,
		Healthy:  true,
	})
	registry.AddChannel(&sigoengine.Channel{
		Provider: "mammouth",
		Name:     "0",
		APIKey:   "key-0",
		Active:   false,
		Order:    1,
		Healthy:  false,
	})

	return &Server{
		models:         map[string]ModelInfo{},
		memory:         sigoengine.MemoryBlock{},
		breakers:       make(map[string]*sigoengine.EnhancedCircuitBreaker),
		systemPrompt:   "",
		usage:          make(map[string]*ModelUsageStats),
		usageByChannel: make(map[string]*ModelUsageStats),
		channelManager: sigoengine.NewChannelManager(registry),
		baseDir:        dir,
	}, dir
}

func TestHandleHelp_ListsMessagesEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/help", nil)
	rr := httptest.NewRecorder()
	srv.handleHelp(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var help map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &help); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	endpoints, ok := help["endpoints"].([]interface{})
	if !ok {
		t.Fatalf("expected endpoints array, got %+v", help["endpoints"])
	}
	found := false
	for _, e := range endpoints {
		entry, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if entry["path"] == "/v1/messages" && entry["method"] == "POST" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected /api/help to list POST /v1/messages, got: %s", rr.Body.String())
	}
}

func TestHandleChatCompletions_MidStreamFailureDoesNotDoubleWriteOrGlueJSON(t *testing.T) {
	// Regression for Finding #3 (handleChatCompletions/streamProviderResponse
	// side): once headers are written and at least one SSE chunk flushed,
	// a later read error from the upstream must not (a) retry the next
	// channel (second stream preamble on the same ResponseWriter) nor
	// (b) fall through to the JSON error writer on the already-open
	// text/event-stream body.
	postCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			// sigoengine.PingProvider's pre-flight check.
			w.WriteHeader(http.StatusOK)
			return
		}
		postCalls++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("upstream ResponseWriter does not support flushing")
		}
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"Hallo"}}]}`)
		flusher.Flush()

		// Verbindung mitten im Stream abrupt kappen -> echter Lesefehler
		// (unexpected EOF) statt eines sauberen Stream-Endes.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("upstream ResponseWriter does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		conn.Close()
	}))
	defer upstream.Close()

	srv, _ := newTestServer(t)
	// Zweiten aktiven Kanal für denselben Provider aktivieren, damit ohne
	// den Fix tatsächlich ein Failover-Versuch stattfinden würde.
	if err := srv.channelManager.Registry().SetActive("mammouth", "0", true); err != nil {
		t.Fatalf("failed to activate second channel: %v", err)
	}
	srv.models = map[string]ModelInfo{
		"claude-h": {ID: "claude-h", Endpoint: upstream.URL, MaxOutputTokens: 100, MaxTemperature: 1, MinTemperature: 0},
	}

	body := `{"model":"claude-h","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rr := httptest.NewRecorder()

	srv.handleChatCompletions(rr, req)

	if postCalls != 1 {
		t.Fatalf("expected exactly 1 upstream POST call (no failover retry once the stream had started), got %d", postCalls)
	}
	respBody := rr.Body.String()
	if n := strings.Count(respBody, `"delta":{"content":"Hallo"}`); n != 1 {
		t.Fatalf("expected exactly 1 streamed chunk (no duplicate preamble/retry), got %d in body:\n%s", n, respBody)
	}
	if strings.Contains(respBody, `"error"`) {
		t.Fatalf("expected no JSON error glued onto the open SSE stream, got body:\n%s", respBody)
	}
}

func TestHandleChannels(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	rr := httptest.NewRecorder()
	srv.handleChannels(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var channels []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &channels); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(channels))
	}
}

func TestHandleChannelDetail(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/channels/mammouth/default", nil)
	rr := httptest.NewRecorder()
	srv.handleChannelRouter(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var detail map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if detail["name"] != "default" {
		t.Fatalf("expected default channel, got %v", detail["name"])
	}
}

func TestHandleChannelEnableDisable(t *testing.T) {
	srv, dir := newTestServer(t)

	// Enable channel 0
	req := httptest.NewRequest(http.MethodPost, "/api/channels/mammouth/0/enable", nil)
	rr := httptest.NewRecorder()
	srv.handleChannelRouter(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	ch, _ := srv.channelManager.Registry().GetChannel("mammouth", "0")
	if !ch.Active {
		t.Fatal("expected channel 0 to be active")
	}

	// State persisted
	if _, err := os.Stat(filepath.Join(dir, "channels.json")); err != nil {
		t.Fatalf("channels.json not written: %v", err)
	}

	// Disable channel 0
	req = httptest.NewRequest(http.MethodPost, "/api/channels/mammouth/0/disable", nil)
	rr = httptest.NewRecorder()
	srv.handleChannelRouter(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	ch, _ = srv.channelManager.Registry().GetChannel("mammouth", "0")
	if ch.Active {
		t.Fatal("expected channel 0 to be inactive")
	}
}

func TestHandleChannelMemory(t *testing.T) {
	srv, _ := newTestServer(t)

	body := `{"content":"kanal memory","cache":false}`
	req := httptest.NewRequest(http.MethodPut, "/api/channels/mammouth/default/memory", bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	srv.handleChannelRouter(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/channels/mammouth/default/memory", nil)
	rr = httptest.NewRecorder()
	srv.handleChannelRouter(rr, req)

	if !strings.Contains(rr.Body.String(), "kanal memory") {
		t.Fatalf("expected memory content, got %s", rr.Body.String())
	}
}

func TestHandleChannelSystemPrompt(t *testing.T) {
	srv, _ := newTestServer(t)

	body := `{"system_prompt":"kanal prompt"}`
	req := httptest.NewRequest(http.MethodPut, "/api/channels/mammouth/default/system-prompt", bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	srv.handleChannelRouter(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/channels/mammouth/default/system-prompt", nil)
	rr = httptest.NewRecorder()
	srv.handleChannelRouter(rr, req)

	if !strings.Contains(rr.Body.String(), "kanal prompt") {
		t.Fatalf("expected system prompt, got %s", rr.Body.String())
	}
}

func TestHandleVersion(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	rr := httptest.NewRecorder()
	srv.handleVersion(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), sigoengine.Version) {
		t.Fatalf("expected version %s, got %s", sigoengine.Version, rr.Body.String())
	}
}

func TestHandleUsage(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
	rr := httptest.NewRecorder()
	srv.handleUsage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var usage map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &usage); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := usage["by_model"]; !ok {
		t.Fatal("missing by_model")
	}
	if _, ok := usage["by_channel"]; !ok {
		t.Fatal("missing by_channel")
	}
}

func TestRecordUsage(t *testing.T) {
	srv, _ := newTestServer(t)
	ch := &sigoengine.Channel{Provider: "mammouth", Name: "default"}

	srv.recordUsage("claude-h", ch, &sigoengine.UsageData{InputTokens: 10, OutputTokens: 5, TotalTokens: 15})
	srv.recordUsage("claude-h", ch, &sigoengine.UsageData{InputTokens: 3, OutputTokens: 2, TotalTokens: 5})

	stats := srv.usage["claude-h"]
	if stats == nil || stats.Requests != 2 || stats.TotalTokens != 20 {
		t.Fatalf("unexpected model stats: %+v", stats)
	}

	chStats := srv.usageByChannel["claude-h#mammouth-default"]
	if chStats == nil || chStats.Requests != 2 || chStats.TotalTokens != 20 {
		t.Fatalf("unexpected channel stats: %+v", chStats)
	}
}

func TestLookupModel_ChannelSuffixAndRetired(t *testing.T) {
	srv, dir := newTestServer(t)

	reg, err := sigoengine.OpenIDRegistry(dir)
	if err != nil {
		t.Fatalf("OpenIDRegistry: %v", err)
	}
	t.Cleanup(func() { reg.Close() })
	srv.idRegistry = reg

	active, err := reg.AssignModel("zai", "glm-4.5", "")
	if err != nil {
		t.Fatalf("AssignModel(active): %v", err)
	}
	srv.models["glm-4.5"] = ModelInfo{ID: "glm-4.5", Shortcode: active.Shortcode}

	retired, err := reg.AssignModel("zai", "glm-4.4-old", "")
	if err != nil {
		t.Fatalf("AssignModel(retired): %v", err)
	}
	if _, err := reg.SyncProvider("zai", []sigoengine.ProviderModelSeed{{UpstreamID: "glm-4.5"}}); err != nil {
		t.Fatalf("SyncProvider: %v", err)
	}
	for i := 0; i < 2; i++ { // insgesamt 3 Misses inkl. des Sync oben
		if _, err := reg.SyncProvider("zai", []sigoengine.ProviderModelSeed{{UpstreamID: "glm-4.5"}}); err != nil {
			t.Fatalf("SyncProvider (miss %d): %v", i, err)
		}
	}

	t.Run("exact ID match", func(t *testing.T) {
		lr, ok := srv.lookupModel("glm-4.5")
		if !ok || lr.ID != "glm-4.5" || lr.Retired {
			t.Fatalf("lookupModel(glm-4.5) = %+v, ok=%v", lr, ok)
		}
	})

	t.Run("channel suffix on shortcode", func(t *testing.T) {
		lr, ok := srv.lookupModel(active.Shortcode + "-2")
		if !ok {
			t.Fatal("lookupModel mit Kanal-Suffix nicht gefunden")
		}
		if lr.ID != "glm-4.5" {
			t.Errorf("ID = %q, erwartet glm-4.5", lr.ID)
		}
		if lr.Channel != "2" {
			t.Errorf("Channel = %q, erwartet '2'", lr.Channel)
		}
	})

	t.Run("retired model resolves with Retired flag", func(t *testing.T) {
		lr, ok := srv.lookupModel(retired.Shortcode)
		if !ok {
			t.Fatal("lookupModel für retired Shortcode nicht gefunden")
		}
		if !lr.Retired {
			t.Error("erwarte Retired == true")
		}
		if lr.RetiredAt.IsZero() {
			t.Error("erwarte gesetztes RetiredAt")
		}
	})

	t.Run("unknown shortcode", func(t *testing.T) {
		_, ok := srv.lookupModel("does-not-exist")
		if ok {
			t.Error("erwarte ok=false für unbekannten Shortcode")
		}
	})
}

// **********************************************************************
// Tests für POST /v1/embeddings
// **********************************************************************

// fakeOllamaConfig steuert das Verhalten des Test-Ollama-Servers.
type fakeOllamaConfig struct {
	embedCount int    // Anzahl Embeddings die /api/embed zurückgibt (-1 = len(input))
	modelName  string // Ollama-Modellname für /api/tags (Default: nomic-embed-text-v2-moe:latest)
}

// startFakeOllama startet einen httptest.Server, der /api/tags, /api/show
// und /api/embed bedient — als Test-Double für echtes Ollama.
func startFakeOllama(t *testing.T, cfg fakeOllamaConfig) *httptest.Server {
	t.Helper()
	if cfg.modelName == "" {
		cfg.modelName = "nomic-embed-text-v2-moe:latest"
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"models": []map[string]interface{}{
					{"name": cfg.modelName, "size": int64(957680763)},
				},
			})
		case "/api/show":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"modelinfo":{}}`))
		case "/v1/chat/completions":
			if r.Method == http.MethodHead { // PingProvider-Preflight
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.Header.Get("Authorization") != "" {
				t.Errorf("fake ollama: unerwarteter Authorization-Header %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"c1","object":"chat.completion","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":"Hallo von Ollama"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`))
		case "/api/embed":
			var req struct {
				Model string   `json:"model"`
				Input []string `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("fake ollama: invalid /api/embed body: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			count := cfg.embedCount
			if count < 0 {
				count = len(req.Input)
			}
			embeddings := make([][]float64, count)
			for i := range embeddings {
				embeddings[i] = []float64{float64(i) + 0.1, float64(i) + 0.2, float64(i) + 0.3}
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"model":      req.Model,
				"embeddings": embeddings,
			})
		default:
			http.NotFound(w, r)
		}
	}))

	return srv
}

// setupEmbeddingTestServer richtet einen Test-Server mit Fake-Ollama ein.
// Gibt (server, shortcode) zurück. Räumt ollamaEndpoint und Ollama-Registry
// automatisch per t.Cleanup auf.
func setupEmbeddingTestServer(t *testing.T, cfg fakeOllamaConfig) (*Server, string) {
	t.Helper()

	fake := startFakeOllama(t, cfg)
	t.Cleanup(fake.Close)

	// Ollama-Endpoint auf Fake umleiten
	oldEndpoint := ollamaEndpoint
	ollamaEndpoint = fake.URL
	t.Cleanup(func() { ollamaEndpoint = oldEndpoint })

	// Ollama-Discovery über Fake ausführen (füllt die globale Registry)
	sigoengine.DiscoverOllamaModels(fake.URL)
	t.Cleanup(func() {
		// Registry aufräumen: unreachable Endpoint leert sie
		sigoengine.DiscoverOllamaModels("http://127.0.0.1:1")
	})

	srv, _ := newTestServer(t)

	// Gefundene Ollama-Modelle in srv.models übernehmen (wie main() es tut)
	for sc := range sigoengine.GetOllamaModels() {
		srv.models[sc] = ModelInfo{
			ID:        sc,
			Shortcode: sc,
			Endpoint:  fake.URL + "/v1/chat/completions",
		}
	}

	var shortcode string
	for sc := range sigoengine.GetOllamaModels() {
		shortcode = sc
		break
	}

	return srv, shortcode
}

func TestHandleEmbeddings_SingleInput(t *testing.T) {
	srv, shortcode := setupEmbeddingTestServer(t, fakeOllamaConfig{embedCount: -1})

	body := fmt.Sprintf(`{"model":"%s","input":"hallo welt"}`, shortcode)
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleEmbeddings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp EmbeddingResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Object != "list" {
		t.Errorf("object = %q, expected 'list'", resp.Object)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 embedding, got %d", len(resp.Data))
	}
	if resp.Data[0].Object != "embedding" {
		t.Errorf("data[0].object = %q, expected 'embedding'", resp.Data[0].Object)
	}
	if resp.Data[0].Index != 0 {
		t.Errorf("data[0].index = %d, expected 0", resp.Data[0].Index)
	}
	if len(resp.Data[0].Embedding) != 3 {
		t.Errorf("embedding dim = %d, expected 3", len(resp.Data[0].Embedding))
	}
	if resp.Model == "" {
		t.Error("model field is empty")
	}
}

// TestHandleEmbeddings_ModelRoundTrip stellt sicher, dass response.model exakt
// dem Shortcode/Modell-ID entspricht, den der Client geschickt hat — nicht dem
// internen OllamaName (z.B. "nomic-embed-text-v2-moe:latest"). OpenAI-Vertrag:
// was der Client als model schickt, bekommt er als model zurück.
func TestHandleEmbeddings_ModelRoundTrip(t *testing.T) {
	srv, shortcode := setupEmbeddingTestServer(t, fakeOllamaConfig{embedCount: -1})

	body := fmt.Sprintf(`{"model":"%s","input":"hallo welt"}`, shortcode)
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleEmbeddings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp EmbeddingResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Model != shortcode {
		t.Errorf("response.model = %q, expected shortcode %q (round-trip)", resp.Model, shortcode)
	}
	if strings.HasSuffix(resp.Model, ":latest") {
		t.Errorf("response.model = %q darf nicht das ollama :latest-Suffix enthalten", resp.Model)
	}
}

func TestHandleEmbeddings_ArrayInput(t *testing.T) {
	srv, shortcode := setupEmbeddingTestServer(t, fakeOllamaConfig{embedCount: -1})

	body := fmt.Sprintf(`{"model":"%s","input":["text eins","text zwei","text drei"]}`, shortcode)
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleEmbeddings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp EmbeddingResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("expected 3 embeddings, got %d", len(resp.Data))
	}
	for i, d := range resp.Data {
		if d.Index != i {
			t.Errorf("data[%d].index = %d, expected %d", i, d.Index, i)
		}
		if d.Object != "embedding" {
			t.Errorf("data[%d].object = %q, expected 'embedding'", i, d.Object)
		}
	}
}

func TestHandleEmbeddings_UnknownModel(t *testing.T) {
	srv, _ := setupEmbeddingTestServer(t, fakeOllamaConfig{embedCount: -1})

	body := `{"model":"does-not-exist","input":"hallo"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleEmbeddings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleEmbeddings_EmptyInput(t *testing.T) {
	srv, shortcode := setupEmbeddingTestServer(t, fakeOllamaConfig{embedCount: -1})

	cases := []struct {
		name string
		body string
	}{
		{"empty string", fmt.Sprintf(`{"model":"%s","input":""}`, shortcode)},
		{"empty array", fmt.Sprintf(`{"model":"%s","input":[]}`, shortcode)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(tc.body))
			rr := httptest.NewRecorder()
			srv.handleEmbeddings(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHandleEmbeddings_NonOllamaModel(t *testing.T) {
	// Sicherstellen, dass die Ollama-Registry leer ist (kein false-positive Match).
	sigoengine.DiscoverOllamaModels("http://127.0.0.1:1")

	srv, _ := newTestServer(t)
	srv.models["claude-h"] = ModelInfo{
		ID:        "claude-h",
		Shortcode: "claude-h",
		Endpoint:  "https://api.example.com/v1/chat/completions",
	}

	body := `{"model":"claude-h","input":"hallo"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleEmbeddings(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "ollama") {
		t.Fatalf("expected error mentioning ollama, got: %s", rr.Body.String())
	}
}

func TestHandleEmbeddings_OllamaDown(t *testing.T) {
	srv, shortcode := setupEmbeddingTestServer(t, fakeOllamaConfig{embedCount: -1})

	// ollamaEndpoint auf tote Adresse umstellen (Registry bleibt erhalten).
	ollamaEndpoint = "http://127.0.0.1:1"

	body := fmt.Sprintf(`{"model":"%s","input":"hallo"}`, shortcode)
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleEmbeddings(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleEmbeddings_WrongEmbeddingCount(t *testing.T) {
	// Fake gibt immer 1 Embedding zurück, unabhängig von Input-Anzahl.
	srv, shortcode := setupEmbeddingTestServer(t, fakeOllamaConfig{embedCount: 1})

	body := fmt.Sprintf(`{"model":"%s","input":["a","b"]}`, shortcode)
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleEmbeddings(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleEmbeddings_WrongMethod(t *testing.T) {
	srv, _ := setupEmbeddingTestServer(t, fakeOllamaConfig{embedCount: -1})

	req := httptest.NewRequest(http.MethodGet, "/v1/embeddings", nil)
	rr := httptest.NewRecorder()
	srv.handleEmbeddings(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestHandleHelp_ListsEmbeddingsEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/help", nil)
	rr := httptest.NewRecorder()
	srv.handleHelp(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var help map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &help); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	endpoints, ok := help["endpoints"].([]interface{})
	if !ok {
		t.Fatalf("expected endpoints array, got %+v", help["endpoints"])
	}
	found := false
	for _, e := range endpoints {
		entry, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if entry["path"] == "/v1/embeddings" && entry["method"] == "POST" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected /api/help to list POST /v1/embeddings, got: %s", rr.Body.String())
	}
}

// TestHandleChatCompletions_OllamaWithoutAPIKey: Regression — Ollama hat
// keinen API-Key, bekam deshalb in DiscoverFromEnv keinen Kanal, und jeder
// Chat-Call endete in ChannelManager.Resolve mit "no active channel for
// provider", bevor Ollama überhaupt angefragt wurde. Die Registry wird hier
// über denselben Weg gebaut wie in main() (newChannelRegistry).
func TestHandleChatCompletions_OllamaWithoutAPIKey(t *testing.T) {
	srv, shortcode := setupEmbeddingTestServer(t, fakeOllamaConfig{modelName: "llama3:latest"})
	srv.channelManager = sigoengine.NewChannelManager(newChannelRegistry(srv.baseDir, true))
	srv.rateLimiter = sigoengine.NewRateLimiter()

	body := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hi"}]}`, shortcode)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleChatCompletions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Status %d, Body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Hallo von Ollama") {
		t.Fatalf("Antwort fehlt: %s", rr.Body.String())
	}
}

// TestNewChannelRegistry_NoOllamaNoChannel: ohne gefundene Ollama-Modelle
// wird kein Ollama-Kanal registriert (kein toter Eintrag in /api/channels).
func TestNewChannelRegistry_NoOllamaNoChannel(t *testing.T) {
	r := newChannelRegistry(t.TempDir(), false)
	if n := len(r.Channels("ollama")); n != 0 {
		t.Fatalf("erwartet 0 Ollama-Kanäle, bekommen %d", n)
	}
}

// TestStreamProviderResponse_ExactlyOneDone: Regression — der Server reichte
// das [DONE] des Upstreams durch und hängte danach ein eigenes an, Clients
// sahen "data: [DONE]" zweimal. Sendet der Upstream keins, muss der Server
// es weiterhin selbst ergänzen.
func TestStreamProviderResponse_ExactlyOneDone(t *testing.T) {
	cases := map[string]string{
		"upstream mit [DONE]": "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\ndata: [DONE]\n\n",
		"upstream ohne [DONE]": "data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n",
	}
	for name, upstream := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := newTestServer(t)
			rr := httptest.NewRecorder()
			text, err := srv.streamProviderResponse(rr, io.NopCloser(strings.NewReader(upstream)), "m")
			if err != nil {
				t.Fatal(err)
			}
			if text != "Hi" {
				t.Errorf("Text = %q", text)
			}
			if n := strings.Count(rr.Body.String(), "data: [DONE]"); n != 1 {
				t.Fatalf("erwartet genau 1x [DONE], bekommen %d:\n%s", n, rr.Body.String())
			}
		})
	}
}

// TestHandleChannelDisable_IsManual: /disable über die API setzt das
// Manuell-Flag (Health-Monitor lässt den Kanal dann in Ruhe), /enable löscht es.
func TestHandleChannelDisable_IsManual(t *testing.T) {
	srv, _ := newTestServer(t)
	reg := srv.channelManager.Registry()

	rr := httptest.NewRecorder()
	srv.handleChannelDisable(rr, httptest.NewRequest(http.MethodPost, "/api/channels/mammouth/default/disable", nil), "mammouth", "default")
	if ch, _ := reg.GetChannel("mammouth", "default"); ch.Active || !ch.ManuallyDisabled {
		t.Fatalf("nach /disable: active=%v manual=%v", ch.Active, ch.ManuallyDisabled)
	}

	// Flag muss auch in der API-Ausgabe sichtbar sein (Liste + Detail).
	rr = httptest.NewRecorder()
	srv.handleChannels(rr, httptest.NewRequest(http.MethodGet, "/api/channels", nil))
	var list []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range list {
		if c["full_name"] == "mammouth-default" {
			found = true
			if c["manually_disabled"] != true {
				t.Errorf("/api/channels: manually_disabled = %v, erwartet true", c["manually_disabled"])
			}
		}
	}
	if !found {
		t.Fatal("mammouth-default fehlt in /api/channels")
	}
	rr = httptest.NewRecorder()
	srv.handleChannelDetail(rr, httptest.NewRequest(http.MethodGet, "/api/channels/mammouth/default", nil), "mammouth", "default")
	var detail map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["manually_disabled"] != true {
		t.Errorf("Detail: manually_disabled = %v, erwartet true", detail["manually_disabled"])
	}

	rr = httptest.NewRecorder()
	srv.handleChannelEnable(rr, httptest.NewRequest(http.MethodPost, "/api/channels/mammouth/default/enable", nil), "mammouth", "default")
	if ch, _ := reg.GetChannel("mammouth", "default"); !ch.Active || ch.ManuallyDisabled {
		t.Fatalf("nach /enable: active=%v manual=%v", ch.Active, ch.ManuallyDisabled)
	}
}
