//**********************************************************************
//      sigoengine/channel_health.go
//**********************************************************************
//  Beschreibung: Hintergrund-Health-Monitor für Kanäle
//**********************************************************************

package sigoengine

import (
	"context"
	"time"
)

// StartHealthMonitor starts a goroutine that periodically re-evaluates channel
// health and activates reserve channels when needed.
//
// LAZY Health-Modell (Fix gegen API-Kosten ohne User-Request):
// Aktive Kanäle werden NICHT mehr aktiv per Ticker angepingt. Ihr Health-Status
// wird aus echten User-Requests gesetzt (handleChatCompletions → MarkChannelHealth).
// Der Ticker aktiviert nur noch Reserve-Kanäle, wenn alle aktiven unhealthy sind
// oder gar kein aktiver Kanal existiert. Vor der Aktivierung wird die Reserve
// per kostenlosem /models-GET (ProbeProviderModelList) geprüft — das verursacht
// keine Token-Kosten. Auth-fehlgeschlagene Reserven werden deaktiviert.
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
			// KEIN checkChannel mehr auf aktive Kanäle — Health kommt lazy
			// aus User-Requests. Verwendet den vorhandenen Healthy-Status.
			if ch.Healthy {
				allActiveUnhealthy = false
			}
		}

		// Reserve aktivieren, wenn alle aktiven unhealthy sind ODER gar kein
		// aktiver Kanal existiert (z.B. nach Auth-Fail-Deaktivierung).
		needReserve := (!hasActive || allActiveUnhealthy) && firstInactive != nil
		if !needReserve {
			continue
		}

		// Reserve vor Aktivierung kostenlos prüfen (/models-GET, keine Token).
		if checkChannel(firstInactive, registry) {
			LogInfo("Auto-enabling healthy reserve channel", map[string]interface{}{
				"provider": provider,
				"channel":  firstInactive.Name,
			})
			registry.SetActive(provider, firstInactive.Name, true)
		}
	}
}

// checkChannel testet einen Kanal per kostenlosem /models-GET und aktualisiert
// seinen Health-Status via MarkChannelHealth. Gibt true zurück, wenn der Kanal
// verfügbar ("available") ist. Auth-fehlgeschlagene Kanäle werden deaktiviert.
//
// Wird nur für Reserve-Kanäle vor der Aktivierung gerufen — nicht für bereits
// aktive Kanäle (deren Health lazy aus User-Requests stammt).
func checkChannel(ch *Channel, registry *ChannelRegistry) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	health := ProbeProviderModelList(ctx, ch.Provider, ch.APIKey)
	registry.MarkChannelHealth(ch.Provider, ch.Name, health.Status == "available", health.Error)

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
		return false
	}

	return health.Status == "available"
}
