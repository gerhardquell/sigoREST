//**********************************************************************
//      sigoengine/channel_health.go
//**********************************************************************
//  Beschreibung: Hintergrund-Health-Monitor für Kanäle
//**********************************************************************

package sigoengine

import (
	"context"
	"strings"
	"time"
)

// StartHealthMonitor starts a goroutine that periodically health-checks all
// active channels. It activates inactive reserve channels when all active
// channels for a provider become unhealthy. It disables channels on auth errors.
func StartHealthMonitor(ctx context.Context, manager *ChannelManager, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runHealthChecks(manager)
			}
		}
	}()
}

func runHealthChecks(manager *ChannelManager) {
	registry := manager.Registry()
	for _, provider := range registry.AllProviders() {
		allActiveUnhealthy := true
		hasActive := false
		var firstInactive *Channel

		for _, ch := range registry.Channels(provider) {
			if !ch.Active {
				if firstInactive == nil {
					firstInactive = ch
				}
				continue
			}
			hasActive = true
			checkChannel(ch)
			if ch.Healthy {
				allActiveUnhealthy = false
			}
		}

		if hasActive && allActiveUnhealthy && firstInactive != nil {
			LogInfo("Auto-enabling reserve channel", map[string]interface{}{
				"provider": provider,
				"channel":  firstInactive.Name,
			})
			registry.SetActive(provider, firstInactive.Name, true)
		}
	}
}

func checkChannel(ch *Channel) {
	// Look up the first model for the provider to get the endpoint.
	// This is a best-effort probe; if no model is known, skip the check.
	var endpoint string
	for _, m := range GetAllModels() {
		switch {
		case ch.Provider == "mammouth" && strings.Contains(m.Endpoint, "mammouth"):
			endpoint = m.Endpoint
		case ch.Provider == "moonshot" && strings.Contains(m.Endpoint, "moonshot"):
			endpoint = m.Endpoint
		case ch.Provider == "zai" && strings.Contains(m.Endpoint, "z.ai"):
			endpoint = m.Endpoint
		}
		if endpoint != "" {
			break
		}
	}
	if endpoint == "" {
		ch.LastError = "no model endpoint known for provider"
		ch.ConsecutiveErrors++
		return
	}

	cfg := &ProviderConfig{
		Endpoint: endpoint,
		Model:    ch.Provider,
		APIKey:   ch.APIKey,
		Type:     "mammoth",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	health := ProbeProvider(ctx, cfg)
	ch.LastHealthCheck = time.Now()
	if health.Status == "available" {
		ch.Healthy = true
		ch.LastError = ""
		ch.ConsecutiveErrors = 0
		return
	}

	ch.Healthy = false
	ch.LastError = health.Error
	ch.ConsecutiveErrors++

	apiErr := ClassifyError(NewError(health.Error, health.Error, nil, nil))
	if apiErr.Type == ErrAuthFailed {
		LogWarn("Disabling channel due to auth failure", map[string]interface{}{
			"provider": ch.Provider,
			"channel":  ch.Name,
		})
		ch.Active = false
	}
}
