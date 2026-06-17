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
