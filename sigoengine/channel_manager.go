//**********************************************************************
//      sigoengine/channel_manager.go
//**********************************************************************
//  Beschreibung: Kanal-Auflösung und Failover-Logik
//**********************************************************************

package sigoengine

// ErrChannelInactive signals that a specifically requested channel is not active.
const ErrChannelInactive = "CHANNEL_INACTIVE"

// ChannelManager wraps a registry and provides resolution/failover helpers.
type ChannelManager struct {
	registry *ChannelRegistry
}

// NewChannelManager creates a manager for the given registry.
func NewChannelManager(registry *ChannelRegistry) *ChannelManager {
	return &ChannelManager{registry: registry}
}

// Registry returns the underlying registry.
func (m *ChannelManager) Registry() *ChannelRegistry {
	return m.registry
}

// Resolve picks a channel for a provider.
// If requested is empty, returns the first active channel in order.
// If requested is a full name like "mammouth-0", resolves via FullName.
// Otherwise treats requested as the channel name within the provider.
func (m *ChannelManager) Resolve(provider, requested string) (*Channel, error) {
	if requested != "" {
		// Try full name first, e.g. "mammouth-0"
		if ch, ok := m.registry.GetChannelByFullName(requested); ok {
			if ch.Provider != provider {
				return nil, NewError(ErrConfigNotFound, "channel provider does not match model provider", nil,
					map[string]interface{}{"channel_provider": ch.Provider, "model_provider": provider})
			}
			if !ch.Active {
				return nil, NewError(ErrChannelInactive, "requested channel is inactive", nil,
					map[string]interface{}{"channel": requested})
			}
			return ch, nil
		}

		ch, ok := m.registry.GetChannel(provider, requested)
		if !ok {
			return nil, NewError(ErrConfigNotFound, "channel not found", nil,
				map[string]interface{}{"provider": provider, "channel": requested})
		}
		if !ch.Active {
			return nil, NewError(ErrChannelInactive, "requested channel is inactive", nil,
				map[string]interface{}{"provider": provider, "channel": requested})
		}
		return ch, nil
	}

	for _, ch := range m.registry.Channels(provider) {
		if ch.Active {
			return ch, nil
		}
	}
	return nil, NewError(ErrConfigNotFound, "no active channel for provider", nil,
		map[string]interface{}{"provider": provider})
}

// NextActive returns the next active channel after the given one, in order.
// If after is nil, returns the first active channel.
// The bool return indicates whether a channel was found.
func (m *ChannelManager) NextActive(provider string, after *Channel) (*Channel, bool) {
	channels := m.registry.Channels(provider)
	passed := after == nil
	for _, ch := range channels {
		if !passed {
			if ch.Name == after.Name {
				passed = true
			}
			continue
		}
		if ch.Active {
			return ch, true
		}
	}
	return nil, false
}

// AllChannelStatus returns a snapshot of every known channel.
func (m *ChannelManager) AllChannelStatus() []map[string]interface{} {
	var result []map[string]interface{}
	for _, provider := range m.registry.AllProviders() {
		for _, ch := range m.registry.Channels(provider) {
			result = append(result, map[string]interface{}{
				"provider":           ch.Provider,
				"name":               ch.Name,
				"full_name":          ch.FullName(),
				"active":             ch.Active,
				"healthy":            ch.Healthy,
				"last_health_check":  ch.LastHealthCheck,
				"last_error":         ch.LastError,
				"consecutive_errors": ch.ConsecutiveErrors,
			})
		}
	}
	return result
}
