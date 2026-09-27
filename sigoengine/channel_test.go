//**********************************************************************
//      sigoengine/channel_test.go
//**********************************************************************

package sigoengine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChannelRegistry_DiscoverFromEnv(t *testing.T) {
	os.Setenv("MAMMOUTH_API_KEY", "default-key")
	os.Setenv("MAMMOUTH_API_KEY_0", "key-0")
	os.Setenv("MAMMOUTH_API_KEY_1", "key-1")
	os.Setenv("MOONSHOT_API_KEY", "moon-default")
	defer func() {
		os.Unsetenv("MAMMOUTH_API_KEY")
		os.Unsetenv("MAMMOUTH_API_KEY_0")
		os.Unsetenv("MAMMOUTH_API_KEY_1")
		os.Unsetenv("MOONSHOT_API_KEY")
	}()

	reg := NewChannelRegistry("")
	reg.DiscoverFromEnv()

	mammoth := reg.Channels("mammouth")
	if len(mammoth) != 3 {
		t.Fatalf("expected 3 mammouth channels, got %d", len(mammoth))
	}
	if mammoth[0].Name != "default" || !mammoth[0].Active {
		t.Errorf("default channel should be active, got %+v", mammoth[0])
	}
	if mammoth[1].Name != "0" || mammoth[1].Active {
		t.Errorf("channel 0 should be inactive, got %+v", mammoth[1])
	}
	if mammoth[1].APIKey != "key-0" {
		t.Errorf("channel 0 key mismatch: %q", mammoth[1].APIKey)
	}

	moon := reg.Channels("moonshot")
	if len(moon) != 1 {
		t.Fatalf("expected 1 moonshot channel, got %d", len(moon))
	}
}

func TestChannelRegistry_DiscoverFromEnv_Gaps(t *testing.T) {
	os.Setenv("MAMMOUTH_API_KEY", "default-key")
	os.Setenv("MAMMOUTH_API_KEY_0", "key-0")
	// _1 intentionally missing
	os.Setenv("MAMMOUTH_API_KEY_2", "key-2")
	defer func() {
		os.Unsetenv("MAMMOUTH_API_KEY")
		os.Unsetenv("MAMMOUTH_API_KEY_0")
		os.Unsetenv("MAMMOUTH_API_KEY_2")
	}()

	reg := NewChannelRegistry("")
	reg.DiscoverFromEnv()

	mammoth := reg.Channels("mammouth")
	if len(mammoth) != 3 {
		t.Fatalf("expected 3 mammouth channels, got %d", len(mammoth))
	}
	if mammoth[0].Name != "default" || !mammoth[0].Active {
		t.Errorf("default channel should be active, got %+v", mammoth[0])
	}
	if mammoth[1].Name != "0" || mammoth[1].Active {
		t.Errorf("channel 0 should be inactive, got %+v", mammoth[1])
	}
	if mammoth[1].APIKey != "key-0" {
		t.Errorf("channel 0 key mismatch: %q", mammoth[1].APIKey)
	}
	if mammoth[2].Name != "2" || mammoth[2].Active {
		t.Errorf("channel 2 should be inactive, got %+v", mammoth[2])
	}
	if mammoth[2].APIKey != "key-2" {
		t.Errorf("channel 2 key mismatch: %q", mammoth[2].APIKey)
	}
}

func TestChannelRegistry_LoadSaveState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "channels.json")

	os.Setenv("MAMMOUTH_API_KEY", "default-key")
	os.Setenv("MAMMOUTH_API_KEY_0", "key-0")
	defer func() {
		os.Unsetenv("MAMMOUTH_API_KEY")
		os.Unsetenv("MAMMOUTH_API_KEY_0")
	}()

	reg := NewChannelRegistry(statePath)
	reg.DiscoverFromEnv()
	if err := reg.SetActive("mammouth", "0", true); err != nil {
		t.Fatalf("SetActive failed: %v", err)
	}

	reg2 := NewChannelRegistry(statePath)
	reg2.DiscoverFromEnv()
	if err := reg2.LoadState(); err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}

	ch, ok := reg2.GetChannel("mammouth", "0")
	if !ok || !ch.Active {
		t.Errorf("expected channel 0 to be active after loading state")
	}
}

// TestChannelRegistry_AddKeylessChannel: lokale Provider ohne API-Key (Ollama)
// brauchen trotzdem einen aktiven Kanal, sonst scheitert jeder Chat-Call in
// ChannelManager.Resolve mit "no active channel for provider". Ein per API
// deaktivierter Kanal muss nach LoadState deaktiviert bleiben.
func TestChannelRegistry_AddKeylessChannel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.json")
	r := NewChannelRegistry(path)
	r.AddKeylessChannel("ollama")

	chs := r.Channels("ollama")
	if len(chs) != 1 {
		t.Fatalf("erwartet 1 Kanal, bekommen %d", len(chs))
	}
	ch := chs[0]
	if ch.Name != "default" || !ch.Active || !ch.Healthy || ch.APIKey != "" {
		t.Fatalf("Kanal falsch: %+v", ch)
	}
	if _, err := NewChannelManager(r).Resolve("ollama", ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if err := r.SetActive("ollama", "default", false); err != nil {
		t.Fatal(err)
	}
	r2 := NewChannelRegistry(path)
	r2.AddKeylessChannel("ollama")
	if err := r2.LoadState(); err != nil {
		t.Fatal(err)
	}
	if r2.Channels("ollama")[0].Active {
		t.Fatal("persistiert deaktivierter Kanal wurde nach Neustart wieder aktiv")
	}
}
