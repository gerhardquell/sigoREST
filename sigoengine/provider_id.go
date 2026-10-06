//**********************************************************************
//      sigoengine/provider_id.go
//**********************************************************************
//  Autor    : Gerhard Quell - gquell@skequell.de
//  CoAutor  : Claude (Hermes-Session, Provider-Kennzeichnung)
//  Copyright: 2025 Gerhard Quell - SKEQuell
//**********************************************************************
//  Beschreibung: EINE kanonische Quelle für "welcher Provider steckt
//  hinter diesem Modell/Endpoint" — vorher an drei Stellen unabhängig
//  (und unterschiedlich vollständig) nachgebaut:
//    - sigoREST/main.go:            providerForModel() (Endpoint + Name-Fallback)
//    - cmd/sigoE/main.go:           showModels() (nur Mammoth/Moonshot/Z.ai, Rest "Other")
//    - sigoengine/channel.go:       knownProviders (nur für ENV-Discovery)
//
//  ProviderFromEndpoint/ProviderFromModelID bündeln die Erkennung hier;
//  ProviderCode liefert zusätzlich eine auf genau 5 Zeichen normierte
//  Kurzform fürs UI/CLI (Tabellen, Shortlists) — kürzere Provider-Namen
//  werden mit '_' aufgefüllt, längere hart abgeschnitten. Fester Blick:
//  mammo / moons / zai__ / longc / cheap / ollam.
//**********************************************************************

package sigoengine

import "strings"

// providerCodes ordnet jedem kanonischen Provider-Namen (wie ihn
// providerForModel/ProviderFromEndpoint zurückgeben) seinen normierten
// 5-Zeichen-Code zu. Einzige Stelle, an der diese Kürzel definiert sind.
var providerCodes = map[string]string{
	"mammouth":         "mammo",
	"moonshot":         "moons",
	"zai":              "zai__",
	"longcat":          "longc",
	"cheaperinference": "cheap",
	"ollama":           "ollam",
	"openrouter":       "openr",
}

// ProviderCode normiert einen Provider-Namen auf genau 5 Zeichen:
// bekannte Provider nutzen ihr festes Kürzel (providerCodes), unbekannte
// werden mit '_' aufgefüllt bzw. hart auf 5 Zeichen gekappt — so bleibt
// die Spaltenbreite in Tabellen/CLI-Ausgaben immer exakt 5, auch für
// künftige Provider, die noch kein Kürzel in providerCodes haben.
func ProviderCode(provider string) string {
	if code, ok := providerCodes[provider]; ok {
		return code
	}
	p := strings.ToLower(provider)
	if len(p) >= 5 {
		return p[:5]
	}
	return p + strings.Repeat("_", 5-len(p))
}

// ProviderFromEndpoint erkennt den Provider anhand der Endpoint-URL.
// Liefert "" wenn keine bekannte Provider-Domain enthalten ist — Aufrufer
// entscheiden dann selbst über einen Namens-Fallback (siehe
// ProviderFromModelID) oder einen Default.
func ProviderFromEndpoint(endpoint string) string {
	switch {
	case strings.Contains(endpoint, "mammouth"):
		return "mammouth"
	case strings.Contains(endpoint, "moonshot"):
		return "moonshot"
	case strings.Contains(endpoint, "z.ai"):
		return "zai"
	case strings.Contains(endpoint, "longcat"):
		return "longcat"
	case strings.Contains(endpoint, "cheaperinference"):
		return "cheaperinference"
	case strings.Contains(endpoint, "openrouter"):
		return "openrouter"
	case strings.Contains(endpoint, "localhost:11434"), strings.Contains(endpoint, "127.0.0.1:11434"):
		return "ollama"
	default:
		return ""
	}
}

// ProviderFromModelID errät den Provider aus ID/Shortcode-Namensmustern,
// für Fälle ohne (verlässlichen) Endpoint — z.B. wenn nur die ID bekannt
// ist. Default ist "mammouth" (größter Modell-Pool, historischer
// Fallback aus providerForModel).
func ProviderFromModelID(modelID string) string {
	lower := strings.ToLower(modelID)
	switch {
	case strings.HasPrefix(lower, "ollama-"):
		return "ollama"
	case strings.Contains(lower, "kimi"):
		return "moonshot"
	case strings.Contains(lower, "glm"):
		return "zai"
	case strings.Contains(lower, "longcat"):
		return "longcat"
	case strings.HasPrefix(lower, "ci-"):
		return "cheaperinference"
	case strings.Contains(lower, "/"):
		// OpenRouter-IDs sind "<provider>/<modell>" (z.B. "anthropic/claude-opus-5") —
		// kein anderer Provider nutzt "/" in seinen IDs, eindeutiges Signal.
		return "openrouter"
	default:
		return "mammouth"
	}
}

// ResolveProvider kombiniert beide Signale: Endpoint zuerst (zuverlässiger,
// da vom Server selbst gesetzt), Namens-Heuristik als Fallback. Das ist
// die Logik, die bisher in sigoREST/main.go:providerForModel() unter
// s.mu.RLock() lief — hier lock-frei, damit CLI und Server sie ohne
// Server-State teilen können.
func ResolveProvider(endpoint, modelID string) string {
	if p := ProviderFromEndpoint(endpoint); p != "" {
		return p
	}
	return ProviderFromModelID(modelID)
}
