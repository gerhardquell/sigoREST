package sigoengine

import "time"

// FetchWithRetry ruft fetchFn bis zu attempts-mal auf und wartet zwischen
// Fehlversuchen mit exponentiellem Backoff (baseBackoff, dann ×2, ×4 ...).
//
// Motivation: Beim Systemstart als systemd-Dienst ist DNS evtl. noch nicht
// verfügbar ("no such host"), sodass Provider-Fetches fehlschlagen und nur
// statische Fallback-Modelle geladen werden. network-online.target schützt
// primär davor; dieser Retry ist die zweite Absicherung gegen transiente
// Netz-/DNS-Fehler, ohne die der Dienst bis zum manuellen Restart degradiert
// liefe.
func FetchWithRetry(provider string, attempts int, baseBackoff time.Duration, fetchFn func() ([]Model, error)) ([]Model, error) {
	var lastErr error
	backoff := baseBackoff
	for attempt := 1; attempt <= attempts; attempt++ {
		models, err := fetchFn()
		if err == nil {
			return models, nil
		}
		lastErr = err
		LogWarn("Provider-Fetch fehlgeschlagen, Retry", map[string]interface{}{
			"provider": provider,
			"attempt":  attempt,
			"max":      attempts,
			"error":    err.Error(),
		})
		if attempt < attempts {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return nil, lastErr
}
