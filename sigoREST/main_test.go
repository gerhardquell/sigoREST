package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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

	active, err := reg.AssignModel("zai", "glm-4.5")
	if err != nil {
		t.Fatalf("AssignModel(active): %v", err)
	}
	srv.models["glm-4.5"] = ModelInfo{ID: "glm-4.5", Shortcode: active.Shortcode}

	retired, err := reg.AssignModel("zai", "glm-4.4-old")
	if err != nil {
		t.Fatalf("AssignModel(retired): %v", err)
	}
	if _, err := reg.SyncProvider("zai", []string{"glm-4.5"}); err != nil {
		t.Fatalf("SyncProvider: %v", err)
	}
	for i := 0; i < 2; i++ { // insgesamt 3 Misses inkl. des Sync oben
		if _, err := reg.SyncProvider("zai", []string{"glm-4.5"}); err != nil {
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
