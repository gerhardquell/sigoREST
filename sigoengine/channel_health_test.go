package sigoengine

import (
	"context"
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
