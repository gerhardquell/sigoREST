//**********************************************************************
//      sigoengine/loadconfig_channel.go
//**********************************************************************
//  Beschreibung: LoadConfig-Erweiterung für Kanal-Auswahl
//**********************************************************************

package sigoengine

// LoadConfigWithChannel lädt die Konfiguration für ein Modell unter
// Verwendung eines bestimmten Kanals. Wenn ch nil ist, wird der Default-
// Kanal verwendet (Rückwärtskompatibilität).
func LoadConfigWithChannel(model string, ch *Channel) (*ProviderConfig, error) {
	// Zuerst Ollama-Registry prüfen (shortcode direkt, kein Resolve nötig)
	ollamaRegistryMu.RLock()
	ollamaInfo, isOllama := ollamaRegistry[model]
	ollamaRegistryMu.RUnlock()

	if isOllama {
		return &ProviderConfig{
			Endpoint: "http://localhost:11434/v1/chat/completions",
			Model:    ollamaInfo.OllamaName,
			APIKey:   "", // Ollama braucht keinen Key
			Type:     "ollama",
			Headers:  make(map[string]string),
		}, nil
	}

	// Neue typisierte Registry nutzen
	fullName := ResolveModelName(model)
	m, exists := GetModelByID(fullName)
	if !exists {
		// Fallback für dynamisch geladene Modelle (z.B. glm-4.5 via Z.ai-Fetch),
		// die nicht in der statischen Registry (CSV/CoreModels) stehen. Config wird
		// aus dem Kanal gebaut; der Endpoint wird vom Aufrufer (main.go) über das
		// dynamische modelInfo gesetzt. Der Modellname wird 1:1 durchgereicht, damit
		// Casing und Form zum Provider passen (Z.ai erwartet z.B. lowercase "glm-4.5").
		// Type "mammoth" = OpenAI-kompatibles Bearer-Auth; alle aktuellen Provider
		// (mammouth/moonshot/zai) proxen im OpenAI-Style, siehe CallAPI.
		if ch != nil && ch.APIKey != "" {
			LogDebug("Registry-Miss, nutze Channel-Only Config", map[string]interface{}{
				"model":    model,
				"provider": ch.Provider,
				"channel":  ch.FullName(),
			})
			return &ProviderConfig{
				Endpoint: "", // main.go überschreibt mit modelInfo.Endpoint
				Model:    model,
				APIKey:   ch.APIKey,
				Type:     "mammoth",
				Headers:  make(map[string]string),
			}, nil
		}
		return nil, NewError(ErrConfigNotFound, "Model not found in registry", nil,
			map[string]interface{}{"requested": model, "resolved": fullName})
	}

	apiKey := GetEnvWithFile(m.APIKeyEnv)
	if ch != nil && ch.APIKey != "" {
		apiKey = ch.APIKey
	}
	if apiKey == "" {
		return nil, NewError(ErrAPIKeyMissing, "API key not set", nil,
			map[string]interface{}{"env_var": m.APIKeyEnv, "model": fullName})
	}

	return &ProviderConfig{
		Endpoint: m.Endpoint,
		Model:    fullName,
		APIKey:   apiKey,
		Type:     "mammoth",
		Headers:  make(map[string]string),
	}, nil
}
