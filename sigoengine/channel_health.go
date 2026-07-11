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

// channelModelResolver kann vom Server gesetzt werden, damit der Health-Monitor
// die tatsächlich geladenen Modelle (nicht nur die CLI-Registry) sieht.
var channelModelResolver func(provider string) (endpoint, modelID string)

// SetChannelModelResolver erlaubt dem Server, eine Funktion zu registrieren,
// die pro Provider ein Endpoint/Modell-Paar für Health-Checks liefert.
func SetChannelModelResolver(fn func(provider string) (endpoint, modelID string)) {
	channelModelResolver = fn
}

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
			checkChannel(ch, registry)
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

func checkChannel(ch *Channel, registry *ChannelRegistry) {
	// Look up the first model for the provider to get the endpoint.
	// Server can inject its own model map; otherwise fall back to the CLI
	// registry and known static endpoints.
	var endpoint, modelID string
	if channelModelResolver != nil {
		endpoint, modelID = channelModelResolver(ch.Provider)
	}

	if endpoint == "" || modelID == "" {
		for _, m := range GetAllModels() {
			switch {
			case ch.Provider == "mammouth" && strings.Contains(m.Endpoint, "mammouth"):
				endpoint = m.Endpoint
				modelID = m.ID
			case ch.Provider == "moonshot" && strings.Contains(m.Endpoint, "moonshot"):
				endpoint = m.Endpoint
				modelID = m.ID
			case ch.Provider == "zai" && strings.Contains(m.Endpoint, "z.ai"):
				endpoint = m.Endpoint
				modelID = m.ID
			}
			if endpoint != "" {
				break
			}
		}
	}

	// Fallback auf bekannte statische Endpoints/Modelle, falls Provider-Fetch fehlgeschlagen ist
	if endpoint == "" {
		switch ch.Provider {
		case "moonshot":
			endpoint = moonshotChatEndpoint
			modelID = "moonshot-v1-8k"
		case "zai":
			endpoint = zaiChatEndpoint
			modelID = "glm-4.5"
		}
	}

	if endpoint == "" || modelID == "" {
		ch.LastError = "no model endpoint known for provider"
		ch.ConsecutiveErrors++
		return
	}

	cfg := &ProviderConfig{
		Endpoint: endpoint,
		Model:    modelID,
		APIKey:   ch.APIKey,
		Type:     providerTypeForHealthCheck(ch.Provider, endpoint),
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

	if health.Status == "auth_failed" {
		LogWarn("Disabling channel due to auth failure", map[string]interface{}{
			"provider": ch.Provider,
			"channel":  ch.Name,
		})
		if err := registry.SetActive(ch.Provider, ch.Name, false); err != nil {
			LogWarn("Could not persist channel deactivation", map[string]interface{}{
				"provider": ch.Provider,
				"channel":  ch.Name,
				"error":    err.Error(),
			})
		}
	}
}

// providerTypeForHealthCheck bestimmt den ProviderConfig.Type anhand von
// Provider-Name und Endpoint. Mammouth/Moonshot/Z.ai nutzen OpenAI-Style
// Bearer-Auth (intern "mammoth"), Anthropic x-api-key, Ollama keinen Key.
func providerTypeForHealthCheck(provider, endpoint string) string {
	switch {
	case provider == "anthropic" || strings.Contains(endpoint, "anthropic"):
		return "anthropic"
	case provider == "ollama" || strings.Contains(endpoint, ":11434"):
		return "ollama"
	default:
		return "mammoth"
	}
}
