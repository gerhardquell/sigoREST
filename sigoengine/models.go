//**********************************************************************
//      sigoengine/models.go
//**********************************************************************
//  Beschreibung: Typisierte Modell-Definitionen (Core Models)
//                Minimal-Set als eingebetteter Fallback (5 Modelle)
//                Vollständige Liste in models.csv oder ~/.config/sigorest/models.csv
//**********************************************************************

package sigoengine

// Model repräsentiert eine AI-Modell-Konfiguration
type Model struct {
	ID                       string  // Vollständiger Modellname (z.B. "gpt-4.1")
	Shortcode                string  // Kurzbezeichnung (z.B. "gpt41")
	Endpoint                 string  // API URL
	APIKeyEnv                string  // Environment-Variable für API Key
	MaxInputTokens           int     // Maximale Input-Tokens (Kontextfenster)
	MaxOutputTokens          int     // Maximale Output-Tokens
	InputCost                float64 // Kosten pro 1M Input-Tokens ($)
	OutputCost               float64 // Kosten pro 1M Output-Tokens ($)
	MinTemperature           float64 // Minimale Temperatur
	MaxTemperature           float64 // Maximale Temperatur
	RequiresCompletionTokens bool    // Nutzt max_completion_tokens statt max_tokens (GPT-5)
}

// CoreModels enthält das Minimal-Set eingebetteter Modelle (Fallback)
// Vollständige Liste: sigoREST/models.csv oder ~/.config/sigorest/models.csv
var CoreModels = []Model{
	// 3 Mammouth.ai Modelle
	{ID: "gpt-4.1", Shortcode: "gpt41", Endpoint: "https://api.mammouth.ai/v1/chat/completions", APIKeyEnv: "MAMMOUTH_API_KEY", MaxInputTokens: 128000, MaxOutputTokens: 8192, InputCost: 2.0, OutputCost: 8.0, MinTemperature: 0.0, MaxTemperature: 2.0, RequiresCompletionTokens: false},
	{ID: "claude-sonnet-4-6", Shortcode: "cl-s", Endpoint: "https://api.mammouth.ai/v1/chat/completions", APIKeyEnv: "MAMMOUTH_API_KEY", MaxInputTokens: 200000, MaxOutputTokens: 8192, InputCost: 3.0, OutputCost: 15.0, MinTemperature: 0.0, MaxTemperature: 1.0, RequiresCompletionTokens: false},
	{ID: "claude-opus-4-6", Shortcode: "cl-o", Endpoint: "https://api.mammouth.ai/v1/chat/completions", APIKeyEnv: "MAMMOUTH_API_KEY", MaxInputTokens: 200000, MaxOutputTokens: 8192, InputCost: 15.0, OutputCost: 75.0, MinTemperature: 0.0, MaxTemperature: 1.0, RequiresCompletionTokens: false},

	// 1 Moonshot.ai Modell
	{ID: "kimi-k2.5", Shortcode: "kimi", Endpoint: "https://api.moonshot.ai/v1/chat/completions", APIKeyEnv: "MOONSHOT_API_KEY", MaxInputTokens: 256000, MaxOutputTokens: 4096, InputCost: 0.6, OutputCost: 3.0, MinTemperature: 0.0, MaxTemperature: 2.0, RequiresCompletionTokens: false},

	// 1 Z.ai Modell
	{ID: "GLM-4.5", Shortcode: "zai-glm45", Endpoint: "https://api.z.ai/api/paas/v4/chat/completions", APIKeyEnv: "ZAI_API_KEY", MaxInputTokens: 128000, MaxOutputTokens: 4096, InputCost: 0.6, OutputCost: 2.2, MinTemperature: 0.0, MaxTemperature: 2.0, RequiresCompletionTokens: false},
}
