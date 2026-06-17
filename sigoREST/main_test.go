package main

import (
	"bytes"
	"encoding/json"
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
