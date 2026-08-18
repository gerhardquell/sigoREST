package sigoengine

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// roundTripperFunc lets a test fake HTTP responses without network access.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// withMockHTTPClient swaps the package-level defaultHTTPClient for the test
// and restores it afterwards. The supplied func returns every response.
func withMockHTTPClient(t *testing.T, fn func(*http.Request) (*http.Response, error)) {
	t.Helper()
	orig := defaultHTTPClient
	defaultHTTPClient = &http.Client{Transport: roundTripperFunc(fn)}
	t.Cleanup(func() { defaultHTTPClient = orig })
}

func mockResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// TestRunHealthChecks_AutoEnablesReserve: aktiver Kanal unhealthy (lazy aus
// User-Request) → Reserve wird per kostenlosem /models-Probe geprüft und bei
// "available" aktiviert. Kein Chat-Ping, keine Token-Kosten.
func TestRunHealthChecks_AutoEnablesReserve(t *testing.T) {
	withMockHTTPClient(t, func(*http.Request) (*http.Response, error) {
		return mockResponse(http.StatusOK, `{"data":[]}`), nil
	})

	registry := NewChannelRegistry("")
	registry.AddChannel(&Channel{
		Provider: "mammouth",
		Name:     "default",
		APIKey:   "key-default",
		Active:   true,
		Order:    0,
		Healthy:  false, // lazy: durch User-Request unhealthy geworden
	})
	registry.AddChannel(&Channel{
		Provider: "mammouth",
		Name:     "0",
		APIKey:   "key-0",
		Active:   false,
		Order:    1,
		Healthy:  false,
	})

	manager := NewChannelManager(registry)
	runHealthChecks(manager)

	ch0, ok := registry.GetChannel("mammouth", "0")
	if !ok {
		t.Fatal("reserve channel 0 not found")
	}
	if !ch0.Active {
		t.Fatal("expected reserve channel 0 to be auto-enabled after healthy probe")
	}
	if !ch0.Healthy {
		t.Fatal("expected reserve channel 0 to be healthy after available probe")
	}
}

// TestRunHealthChecks_KeepsReserveInactiveWhenProbeFails: aktiver Kanal
// unhealthy, aber Reserve-Probe "unavailable" → Reserve wird NICHT aktiviert.
func TestRunHealthChecks_KeepsReserveInactiveWhenProbeFails(t *testing.T) {
	withMockHTTPClient(t, func(*http.Request) (*http.Response, error) {
		return mockResponse(http.StatusBadGateway, ""), nil // unavailable
	})

	registry := NewChannelRegistry("")
	registry.AddChannel(&Channel{
		Provider: "moonshot", Name: "default", APIKey: "k", Active: true, Order: 0, Healthy: false,
	})
	registry.AddChannel(&Channel{
		Provider: "moonshot", Name: "0", APIKey: "k0", Active: false, Order: 1,
	})

	runHealthChecks(NewChannelManager(registry))

	ch0, _ := registry.GetChannel("moonshot", "0")
	if ch0.Active {
		t.Fatal("reserve must stay inactive when probe is unavailable")
	}
}

func TestStartHealthMonitor_Interval(t *testing.T) {
	withMockHTTPClient(t, func(*http.Request) (*http.Response, error) {
		return mockResponse(http.StatusOK, `{"data":[]}`), nil
	})

	registry := NewChannelRegistry("")
	registry.AddChannel(&Channel{
		Provider: "mammouth", Name: "default", APIKey: "k", Active: true, Order: 0, Healthy: false,
	})
	registry.AddChannel(&Channel{
		Provider: "mammouth", Name: "0", APIKey: "k0", Active: false, Order: 1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartHealthMonitor(ctx, NewChannelManager(registry), 50*time.Millisecond)

	time.Sleep(200 * time.Millisecond)

	ch0, _ := registry.GetChannel("mammouth", "0")
	if !ch0.Active {
		t.Fatal("expected reserve channel to be auto-enabled by monitor")
	}
}

// TestCheckChannel_AuthFailureDeactivatesChannel: Reserve-Probe liefert 401 →
// auth_failed → Kanal wird deaktiviert + persistiert. Aktive Kanäle werden im
// lazy Modell nicht mehr per Ticker angepingt; Auth-Fail wird hier direkt über
// checkChannel auf einer Reserve geprüft.
func TestCheckChannel_AuthFailureDeactivatesChannel(t *testing.T) {
	withMockHTTPClient(t, func(*http.Request) (*http.Response, error) {
		return mockResponse(http.StatusUnauthorized, ""), nil
	})

	dir := t.TempDir()
	registry := NewChannelRegistry(filepath.Join(dir, "channels.json"))
	registry.AddChannel(&Channel{
		Provider: "moonshot", Name: "0", APIKey: "invalid-key", Active: true, Order: 0, Healthy: true,
	})

	ok := checkChannel(registry.Channels("moonshot")[0], registry)
	if ok {
		t.Fatal("expected checkChannel to return false on auth failure")
	}

	ch, _ := registry.GetChannel("moonshot", "0")
	if ch.Active {
		t.Fatal("expected channel to be deactivated after auth failure")
	}
	if ch.Healthy {
		t.Fatal("expected channel to be unhealthy after auth failure")
	}
	if ch.LastError == "" {
		t.Fatal("expected last error to be recorded")
	}
	if _, err := os.Stat(filepath.Join(dir, "channels.json")); err != nil {
		t.Fatalf("expected channels.json to be written: %v", err)
	}
}
