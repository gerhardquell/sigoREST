package sigoengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunHealthChecks_AutoEnablesReserve(t *testing.T) {
	registry := NewChannelRegistry("")

	// Active but unhealthy channel (unknown provider → no endpoint → health check skipped)
	registry.AddChannel(&Channel{
		Provider: "testprovider",
		Name:     "default",
		APIKey:   "",
		Active:   true,
		Order:    0,
		Healthy:  false,
	})

	// Inactive reserve
	registry.AddChannel(&Channel{
		Provider: "testprovider",
		Name:     "0",
		APIKey:   "key-0",
		Active:   false,
		Order:    1,
		Healthy:  false,
	})

	manager := NewChannelManager(registry)

	runHealthChecks(manager)

	ch0, ok := registry.GetChannel("testprovider", "0")
	if !ok {
		t.Fatal("channel 0 not found")
	}
	if !ch0.Active {
		t.Fatal("expected reserve channel 0 to be auto-enabled")
	}
}

func TestStartHealthMonitor_Interval(t *testing.T) {
	registry := NewChannelRegistry("")
	registry.AddChannel(&Channel{
		Provider:        "testprovider",
		Name:            "default",
		APIKey:          "",
		Active:          true,
		Order:           0,
		Healthy:         false,
		LastHealthCheck: time.Now(),
	})
	registry.AddChannel(&Channel{
		Provider: "testprovider",
		Name:     "0",
		APIKey:   "key-0",
		Active:   false,
		Order:    1,
	})

	manager := NewChannelManager(registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartHealthMonitor(ctx, manager, 50*time.Millisecond)

	// Wait for at least one tick
	time.Sleep(150 * time.Millisecond)

	ch0, _ := registry.GetChannel("testprovider", "0")
	if !ch0.Active {
		t.Fatal("expected reserve channel to be auto-enabled by monitor")
	}
}

func TestCheckChannel_AuthFailureDeactivatesChannel(t *testing.T) {
	dir := t.TempDir()
	registry := NewChannelRegistry(filepath.Join(dir, "channels.json"))

	registry.AddChannel(&Channel{
		Provider: "mammouth",
		Name:     "default",
		APIKey:   "invalid-key",
		Active:   true,
		Order:    0,
		Healthy:  true,
	})

	manager := NewChannelManager(registry)
	runHealthChecks(manager)

	ch, _ := registry.GetChannel("mammouth", "default")
	if ch.Active {
		t.Fatal("expected channel to be deactivated after auth failure")
	}
	if ch.Healthy {
		t.Fatal("expected channel to be unhealthy after auth failure")
	}

	// Persistenz prüfen
	if _, err := os.Stat(filepath.Join(dir, "channels.json")); err != nil {
		t.Fatalf("expected channels.json to be written: %v", err)
	}
}
