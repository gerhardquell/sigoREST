//**********************************************************************
//      sigoengine/channel.go
//**********************************************************************
//  Beschreibung: Kanal-Datenmodell und Registry für Multi-Channel-Support
//**********************************************************************

package sigoengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Channel beschreibt einen API-Key-Kanal eines Providers.
type Channel struct {
	Provider          string    `json:"provider"`
	Name              string    `json:"name"`
	APIKey            string    `json:"-"` // nie serialisieren
	Active            bool      `json:"active"`
	Order             int       `json:"order"`
	Healthy           bool      `json:"healthy"`
	LastHealthCheck   time.Time `json:"last_health_check,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	ConsecutiveErrors int       `json:"consecutive_errors"`
	// Rate-Limit-Config pro Kanal (0 → Server-Default greift).
	MinInterval int `json:"min_interval_ms,omitempty"` // Mindest-Abstand zwischen Calls (ms)
	MaxWait     int `json:"max_wait_ms,omitempty"`     // max Queue-Wartezeit bis 429 (ms)
}

// FullName returns the canonical channel identifier, e.g. "mammouth-0".
func (c *Channel) FullName() string {
	return fmt.Sprintf("%s-%s", c.Provider, c.Name)
}

// ChannelRegistry hält alle bekannten Kanäle pro Provider.
type ChannelRegistry struct {
	mu        sync.RWMutex
	channels  map[string][]*Channel // provider → sorted channels
	statePath string                // path to channels.json
}

// NewChannelRegistry creates an empty registry.
func NewChannelRegistry(statePath string) *ChannelRegistry {
	return &ChannelRegistry{
		channels:  make(map[string][]*Channel),
		statePath: statePath,
	}
}

// Channels returns a copy of all channels for a provider, sorted by Order.
func (r *ChannelRegistry) Channels(provider string) []*Channel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := r.channels[provider]
	result := make([]*Channel, len(list))
	copy(result, list)
	return result
}

// AllProviders returns the list of known providers.
func (r *ChannelRegistry) AllProviders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := make([]string, 0, len(r.channels))
	for p := range r.channels {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	return providers
}

// GetChannel returns a channel by provider and name.
func (r *ChannelRegistry) GetChannel(provider, name string) (*Channel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, ch := range r.channels[provider] {
		if ch.Name == name {
			return ch, true
		}
	}
	return nil, false
}

// GetChannelByFullName returns a channel by its full name, e.g. "mammouth-0".
func (r *ChannelRegistry) GetChannelByFullName(fullName string) (*Channel, bool) {
	parts := strings.SplitN(fullName, "-", 2)
	if len(parts) != 2 {
		return nil, false
	}
	return r.GetChannel(parts[0], parts[1])
}

// SetActive changes the active flag of a channel and persists state.
func (r *ChannelRegistry) SetActive(provider, name string, active bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ch := range r.channels[provider] {
		if ch.Name == name {
			ch.Active = active
			if !active {
				ch.Healthy = false
			}
			return r.saveStateLocked()
		}
	}
	return NewError(ErrConfigNotFound, "channel not found", nil,
		map[string]interface{}{"provider": provider, "channel": name})
}

// AddChannel adds or replaces a channel in the registry.
func (r *ChannelRegistry) AddChannel(ch *Channel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.channels[ch.Provider]
	found := false
	for i, existing := range list {
		if existing.Name == ch.Name {
			list[i] = ch
			found = true
			break
		}
	}
	if !found {
		list = append(list, ch)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Order < list[j].Order
	})
	r.channels[ch.Provider] = list
}

// MarkChannelHealth aktualisiert den Health-Status eines Kanals thread-safe.
// Wird aus zwei Pfaden gerufen:
//   - handleChatCompletions (lazy): nach echtem User-Request → healthy=true
//     bei Erfolg, healthy=false + lastErr bei Fehler.
//   - checkChannel (Reserve-Test im Ticker): nach kostenlosem /models-Probe.
//
// ConsecutiveErrors wird bei healthy=true zurückgesetzt, sonst inkrementiert.
// LastHealthCheck wird immer aktualisiert. Persistiert keinen State (active-Flag
// bleibt unberührt); Persistenz nur via SetActive.
func (r *ChannelRegistry) MarkChannelHealth(provider, name string, healthy bool, lastErr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ch := range r.channels[provider] {
		if ch.Name != name {
			continue
		}
		ch.LastHealthCheck = time.Now()
		if healthy {
			ch.Healthy = true
			ch.LastError = ""
			ch.ConsecutiveErrors = 0
		} else {
			ch.Healthy = false
			ch.LastError = lastErr
			ch.ConsecutiveErrors++
		}
		return
	}
}

// knownProviders maps the base API-key env var names to provider names.
var knownProviders = []struct {
	EnvVar   string
	Provider string
}{
	{"MAMMOUTH_API_KEY", "mammouth"},
	{"MOONSHOT_API_KEY", "moonshot"},
	{"ZAI_API_KEY", "zai"},
}

// DiscoverFromEnv scans environment variables for provider API keys.
// It always registers the default channel (unindexed key) and any indexed
// channels (PROVIDER_API_KEY_0, _1, ...) that are set.
func (r *ChannelRegistry) DiscoverFromEnv() {
	for _, p := range knownProviders {
		// Default channel
		if key := GetEnvWithFile(p.EnvVar); key != "" {
			r.AddChannel(&Channel{
				Provider: p.Provider,
				Name:     "default",
				APIKey:   key,
				Active:   true,
				Order:    0,
				Healthy:  true,
			})
		}

		// Indexed channels: scan a reasonable range and register every key that
		// is set, so gaps like _0 + _2 without _1 still work.
		const maxIndexedChannels = 100
		for i := 0; i < maxIndexedChannels; i++ {
			envName := fmt.Sprintf("%s_%d", p.EnvVar, i)
			key := GetEnvWithFile(envName)
			if key == "" {
				continue
			}
			name := fmt.Sprintf("%d", i)
			r.AddChannel(&Channel{
				Provider: p.Provider,
				Name:     name,
				APIKey:   key,
				Active:   false,
				Order:    i + 1,
				Healthy:  false,
			})
		}
	}
}

// persistedState is the on-disk shape of channels.json.
type persistedState struct {
	Providers map[string]map[string]struct {
		Active      bool `json:"active"`
		MinInterval int  `json:"min_interval_ms,omitempty"`
		MaxWait     int  `json:"max_wait_ms,omitempty"`
	} `json:"providers"`
}

// LoadState reads channels.json and applies saved active flags.
// It never creates API keys; channels must already be discovered from env.
func (r *ChannelRegistry) LoadState() error {
	if r.statePath == "" {
		return nil
	}
	data, err := os.ReadFile(r.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return NewError(ErrSessionError, "cannot read channel state", err,
			map[string]interface{}{"path": r.statePath})
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return NewError(ErrSessionError, "invalid channel state file", err,
			map[string]interface{}{"path": r.statePath})
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for provider, channels := range state.Providers {
		for name, cfg := range channels {
			for _, ch := range r.channels[provider] {
				if ch.Name == name {
					ch.Active = cfg.Active
					ch.MinInterval = cfg.MinInterval
					ch.MaxWait = cfg.MaxWait
					if !ch.Active {
						ch.Healthy = false
					}
				}
			}
		}
	}
	return nil
}

// SaveState persists the current active flags to disk.
func (r *ChannelRegistry) SaveState() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saveStateLocked()
}

func (r *ChannelRegistry) saveStateLocked() error {
	if r.statePath == "" {
		return nil
	}
	state := persistedState{Providers: make(map[string]map[string]struct {
		Active      bool `json:"active"`
		MinInterval int  `json:"min_interval_ms,omitempty"`
		MaxWait     int  `json:"max_wait_ms,omitempty"`
	})}
	for provider, list := range r.channels {
		m := make(map[string]struct {
			Active      bool `json:"active"`
			MinInterval int  `json:"min_interval_ms,omitempty"`
			MaxWait     int  `json:"max_wait_ms,omitempty"`
		})
		for _, ch := range list {
			m[ch.Name] = struct {
				Active      bool `json:"active"`
				MinInterval int  `json:"min_interval_ms,omitempty"`
				MaxWait     int  `json:"max_wait_ms,omitempty"`
			}{Active: ch.Active, MinInterval: ch.MinInterval, MaxWait: ch.MaxWait}
		}
		state.Providers[provider] = m
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return NewError(ErrSessionError, "cannot marshal channel state", err, nil)
	}
	if err := os.MkdirAll(filepath.Dir(r.statePath), 0755); err != nil {
		return NewError(ErrSessionError, "cannot create channel state dir", err,
			map[string]interface{}{"path": r.statePath})
	}
	if err := os.WriteFile(r.statePath, data, 0644); err != nil {
		return NewError(ErrSessionError, "cannot write channel state", err,
			map[string]interface{}{"path": r.statePath})
	}
	return nil
}
