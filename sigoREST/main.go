//**********************************************************************
//      sigoREST/main.go
//**********************************************************************
//  Autor    : Gerhard Quell - gquell@skequell.de
//  CoAutor  : claude sonnet 4.6
//  Copyright: 2025 Gerhard Quell - SKEQuell
//  Erstellt : 20260219
//**********************************************************************
// Beschreibung: REST-Server auf Basis sigoengine Package
//               OpenAI-kompatibler Endpunkt für ~100 parallele Verbindungen
//               IP-basierte Zugriffskontrolle (kein Passwort)
//               Globaler Memory-Block für Prompt-Caching
//**********************************************************************

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "embed"

	"sigorest/sigoengine"
)

// streamWriteDeadlineExtension ist das Zeitfenster, um das ein offener SSE-
// Stream sein Write-Deadline nach jedem Flush verlängert (streamProviderResponse,
// streamAnthropicResponse in anthropic.go). Gleicher Wert wie das WriteTimeout
// der http(s).Server-Konfiguration weiter unten — WriteTimeout bleibt die
// Grenze für normale (nicht-streamende) Antworten; ein Stream erneuert sein
// eigenes Deadline pro Chunk und ist damit nur noch durch echte Stille
// (kein Chunk innerhalb dieses Fensters) limitiert, nicht durch die
// Gesamtlaufzeit (TODO-20261003-kosten.md Punkt 3: brach bisher nach exakt
// 300s mit "unexpected EOF" ab, obwohl der Provider weiter lieferte).
const streamWriteDeadlineExtension = 5 * time.Minute

// streamProviderResponse leitet einen OpenAI-kompatiblen SSE-Stream vom Provider
// an den Client durch und sammelt den Assistant-Text für Sessions. Schiebt,
// wenn der Provider eine usage lieferte, vor dem [DONE]-Terminator einen
// zusätzlichen Chunk mit vollem chatUsage (inkl. cost_usd aus
// inputCostPerM/outputCostPerM) ein — der Provider selbst kennt die
// sigoREST-Preisliste nicht und kann cost_usd nie liefern.
// Gibt den akkumulierten Text, die Provider-usage aus dem letzten Chunk vor
// [DONE] (nil, wenn keine Provider-usage im Stream auftauchte) und einen
// Fehler zurück.
func (s *Server) streamProviderResponse(w http.ResponseWriter, stream io.ReadCloser, model string, inputCostPerM, outputCostPerM, cachedInputCostPerM float64, priceKnown bool) (string, *sigoengine.UsageData, error) {
	defer stream.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return "", nil, fmt.Errorf("response writer does not support flushing")
	}
	rc := http.NewResponseController(w)
	extendWriteDeadline := func() {
		// Nicht jeder ResponseWriter (z.B. httptest.ResponseRecorder ohne
		// eigene SetWriteDeadline-Methode) unterstützt das — Fehler bewusst
		// ignorieren, kein Grund den Stream abzubrechen.
		_ = rc.SetWriteDeadline(time.Now().Add(streamWriteDeadlineExtension))
	}

	var responseText strings.Builder
	var usage *sigoengine.UsageData
	scanner := bufio.NewScanner(stream)
	// Große Chunks unterstützen (z.B. lange JSON-Zeilen)
	const maxScanTokenSize = 1024 * 1024
	buf := make([]byte, 4096)
	scanner.Buffer(buf, maxScanTokenSize)

	for scanner.Scan() {
		line := scanner.Text()

		// "data: [DONE]" nie live durchreichen, sondern nur vermerken: der
		// Terminator wird unten IMMER selbst geschrieben (genau einmal),
		// damit der cost_usd-Chunk (siehe unten) noch davor passt — ein
		// roh durchgereichtes Upstream-[DONE] wäre sonst schon beim Client,
		// bevor wir ueberhaupt wissen, ob es eine usage zum Bepreisen gibt.
		if line == "data: [DONE]" {
			continue
		}

		if _, err := fmt.Fprintln(w, line); err != nil {
			return responseText.String(), usage, err
		}
		flusher.Flush()
		extendWriteDeadline()

		// Text aus data:-Zeilen akkumulieren
		if strings.HasPrefix(line, "data: ") {
			dataStr := strings.TrimPrefix(line, "data: ")
			if dataStr == "" {
				continue
			}
			var chunk map[string]interface{}
			if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
				continue
			}
			if choices, ok := chunk["choices"].([]interface{}); ok && len(choices) > 0 {
				if choice, ok := choices[0].(map[string]interface{}); ok {
					if delta, ok := choice["delta"].(map[string]interface{}); ok {
						if content, ok := delta["content"].(string); ok {
							responseText.WriteString(content)
						}
					}
				}
			}
			// Provider schickt die vollstandige usage typischerweise erst im
			// letzten Chunk vor [DONE] (choices meist leer) - spaeter
			// gesehene usage ueberschreibt eine fruehere bewusst.
			if u := sigoengine.ExtractUsage(chunk, "openai"); u != nil {
				usage = u
			}
		}
	}

	if err := scanner.Err(); err != nil {
		// Upstream-Lesefehler mitten im Stream: Client hat bereits Header +
		// mindestens einen Chunk erhalten. Bestmöglich sauber schließen
		// (dasselbe "data: [DONE]"-Terminator-Pattern wie im Erfolgsfall),
		// statt die Verbindung ohne Abschluss-Marker offen hängen zu lassen.
		// Kein cost_usd-Chunk hier — der Stream endete nicht regulär, die
		// bis dahin gesehene usage ist nicht verlässlich vollständig.
		fmt.Fprintln(w, "data: [DONE]")
		fmt.Fprintln(w)
		flusher.Flush()
		return responseText.String(), usage, err
	}

	// Eigener Chunk mit cost_usd vor [DONE] (TODO-20261003-kosten.md Punkt 2):
	// nur bei sauber beendetem Stream und nur wenn der Provider ueberhaupt
	// usage lieferte - ohne Tokenzahlen gibt es nichts zu bepreisen. Der
	// Provider selbst kennt die sigoREST-Preisliste nicht und kann cost_usd
	// nie liefern.
	if usage != nil {
		if data, err := json.Marshal(map[string]interface{}{
			"choices": []interface{}{},
			"usage":   buildChatUsage(usage, inputCostPerM, outputCostPerM, cachedInputCostPerM, priceKnown),
		}); err == nil {
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			extendWriteDeadline()
		}
	}

	// [DONE]-Terminator: wird jetzt immer genau hier geschrieben (Upstream-
	// [DONE] wurde oben nie durchgereicht) — damit genau einmal, nie doppelt.
	fmt.Fprintln(w, "data: [DONE]")
	fmt.Fprintln(w)
	flusher.Flush()

	return responseText.String(), usage, nil
}

// **********************************************************************
// Embedded Default-Dateien

//go:embed memory.json
var defaultMemoryJSON string

// **********************************************************************
// ModelInfo - Modell-Informationen aus CSV
type ModelInfo struct {
	ID                       string  `json:"id"`
	Shortcode                string  `json:"shortcode"`
	Endpoint                 string  `json:"endpoint"`
	APIKey                   string  `json:"apikey"`
	MaxInputTokens           int     `json:"max_input_tokens"`
	MaxOutputTokens          int     `json:"max_output_tokens"`
	InputCost                float64 `json:"input_cost"`                  // $/1M tokens
	OutputCost               float64 `json:"output_cost"`                 // $/1M tokens
	CachedInputCost          float64 `json:"cached_input_cost,omitempty"` // $/1M Cache-Read-Tokens, 0 = kein bekannter Rabatt
	MinTemperature           float64 `json:"min_temperature"`
	MaxTemperature           float64 `json:"max_temperature"`
	RequiresCompletionTokens bool    `json:"requires_completion_tokens"`
	UpstreamID               string  `json:"upstream_id,omitempty"`   // realer Modellname beim Provider, falls ≠ ID
	Provider                 string  `json:"provider,omitempty"`      // nur in API-Responses befüllt (providerForModel), kein CSV-Feld
	ProviderCode             string  `json:"provider_code,omitempty"` // normierter 5-Zeichen-Code (z.B. "mammo"), nur in API-Responses
}

// ModelUsageStats kumulierter Token-Verbrauch pro Modell
type ModelUsageStats struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
	Requests     int64 `json:"requests"`
}

// **********************************************************************
// Server-State
type Server struct {
	mu              sync.RWMutex
	memory          sigoengine.MemoryBlock
	models          map[string]ModelInfo                          // id → ModelInfo
	breakers        map[string]*sigoengine.EnhancedCircuitBreaker // Modell → Enhanced Circuit Breaker
	systemPrompt    string                                        // globaler Default-Prompt (leer = kein Prompt)
	usageMu         sync.RWMutex
	usage           map[string]*ModelUsageStats // model-id → Stats
	usageByChannel  map[string]*ModelUsageStats // model-id#provider-channel → Stats
	channelManager  *sigoengine.ChannelManager
	rateLimiter     *sigoengine.RateLimiter
	rateMinInterval time.Duration
	rateMaxWait     time.Duration
	baseDir         string
	costDB          *sigoengine.CostDB     // persistentes Kosten-Tracking (costs.db im data-dir); nil-safe
	idRegistry      *sigoengine.IDRegistry // persistente Shortcode-/Provider-Kürzel-Registry (id_registry.db im data-dir); nil-safe
}

// **********************************************************************
// Server-Konfiguration (Flags)
var (
	httpPort              = flag.Int("http-port", 9080, "HTTP-Port für localhost")
	httpsPort             = flag.Int("https-port", 9443, "HTTPS-Port für privates Netz")
	certFile              = flag.String("cert", "./certs/server.crt", "TLS-Zertifikat")
	keyFile               = flag.String("key", "./certs/server.key", "TLS-Schlüssel")
	logLevel              = flag.String("v", "info", "Log-Level: debug|info|warn|error")
	quiet                 = flag.Bool("q", false, "Quiet Mode")
	jsonLogs              = flag.Bool("j", false, "JSON-Logs")
	showVersion           = flag.Bool("version", false, "Version anzeigen")
	dataDir               = flag.String("data-dir", "/var/sigoREST", "Basisverzeichnis für Kanäle, Sessions, Memory, System-Prompts")
	channelHealthInterval = flag.Duration("channel-health-interval", 30*time.Second, "Intervall für Kanal-Health-Checks")
	rateMinInterval       = flag.Duration("rate-min-interval", 500*time.Millisecond, "Default Mindest-Abstand zwischen Calls pro Kanal (0=deaktiviert)")
	rateMaxWait           = flag.Duration("rate-max-wait", 1000*time.Millisecond, "Default max Queue-Wartezeit bis HTTP 429 pro Kanal")
	commLogPath           = flag.String("comm-log", "", "Provider-Kommunikation (Chat+Embeddings, volle Bodies) als JSONL protokollieren, z.B. /var/log/sigoREST/communication.jsonl (leer=aus)")
)

// ollamaEndpoint ist der Default-Endpoint für lokale Ollama-Modelle.
// Paket-Level-Variable (kein Server-Feld), damit Tests den Endpoint auf
// einen httptest.Server umleiten können — minimale Änderung, kein Eingriff
// in die Server-Struct.
var ollamaEndpoint = "http://localhost:11434"

// **********************************************************************
// IP-Zugriffskontrolle

// localhost-Bereich: 127.0.0.0/8
var localhostCIDR *net.IPNet

// Private Netze: 192.168.0.0/16 und 10.0.0.0/8
var privateNets []*net.IPNet

func init() {
	_, localhostCIDR, _ = net.ParseCIDR("127.0.0.0/8")
	_, n1, _ := net.ParseCIDR("192.168.0.0/16")
	_, n2, _ := net.ParseCIDR("10.0.0.0/8")
	privateNets = []*net.IPNet{n1, n2}
}

// extractIP extrahiert die IP-Adresse aus r.RemoteAddr ("ip:port" oder "[ip]:port")
func extractIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}

// isLocalhost prüft ob die IP im 127.0.0.0/8 Bereich oder ::1 liegt
func isLocalhost(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return localhostCIDR.Contains(ip) || ip.Equal(net.IPv6loopback)
}

// isPrivateNet prüft ob die IP in einem privaten Netz liegt
func isPrivateNet(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, cidr := range privateNets {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// ipMiddleware prüft die IP und gibt 403 bei unzulässigem Zugriff
// allowedCheck: Funktion die prüft ob IP erlaubt ist
func ipMiddleware(allowedCheck func(net.IP) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r.RemoteAddr)

		// IPv6-Adressen (außer ::1 loopback) blockieren
		if ip != nil && ip.To4() == nil && !ip.Equal(net.IPv6loopback) {
			sigoengine.LogWarn("IPv6 blocked", map[string]interface{}{"ip": r.RemoteAddr})
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		if !allowedCheck(ip) {
			sigoengine.LogWarn("IP blocked", map[string]interface{}{"ip": r.RemoteAddr, "path": r.URL.Path})
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// serverHeaderMiddleware fügt den Server-Header zu jeder Antwort hinzu
func serverHeaderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", fmt.Sprintf("sigoREST/%s", sigoengine.Version))
		next.ServeHTTP(w, r)
	})
}

// **********************************************************************
// TLS Self-Signed Zertifikat

// ensureTLSCert stellt sicher dass ein TLS-Zertifikat vorhanden ist
func ensureTLSCert(certPath, keyPath string) error {
	// Existierende Zertifikate wiederverwenden
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			sigoengine.LogInfo("TLS-Zertifikat vorhanden", map[string]interface{}{"cert": certPath})
			return nil
		}
	}

	sigoengine.LogInfo("Generiere Self-Signed TLS-Zertifikat")
	os.MkdirAll("./certs", 0700)

	// RSA Key generieren
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("RSA Key Generation: %w", err)
	}

	// Zertifikat-Template
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			Organization: []string{"sigoREST"},
			CommonName:   "sigoREST Server",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// SANs: localhost, 127.0.0.1 und alle privaten IPs hinzufügen
	template.IPAddresses = []net.IP{
		net.ParseIP("127.0.0.1"),
		net.IPv6loopback,
	}
	template.DNSNames = []string{"localhost"}

	// Zertifikat signieren
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("Zertifikat-Erstellung: %w", err)
	}

	// Cert auf Disk schreiben
	certOut, err := os.Create(certPath)
	if err != nil {
		return fmt.Errorf("Cert-Datei: %w", err)
	}
	defer certOut.Close()
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	// Key auf Disk schreiben
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("Key-Datei: %w", err)
	}
	defer keyOut.Close()
	keyBytes, _ := x509.MarshalPKCS8PrivateKey(privateKey)
	pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})

	sigoengine.LogInfo("TLS-Zertifikat erstellt", map[string]interface{}{"cert": certPath, "key": keyPath})
	return nil
}

// **********************************************************************
// Modelle von Provider-APIs laden

// modelInfoFromEngine konvertiert sigoengine.Model → ModelInfo
func modelInfoFromEngine(m sigoengine.Model) ModelInfo {
	info := ModelInfo{
		ID:                       m.ID,
		Shortcode:                m.Shortcode,
		Endpoint:                 m.Endpoint,
		MaxInputTokens:           m.MaxInputTokens,
		MaxOutputTokens:          m.MaxOutputTokens,
		InputCost:                m.InputCost,
		OutputCost:               m.OutputCost,
		CachedInputCost:          m.CachedInputCost,
		MinTemperature:           m.MinTemperature,
		MaxTemperature:           m.MaxTemperature,
		RequiresCompletionTokens: m.RequiresCompletionTokens,
		UpstreamID:               m.UpstreamID,
	}
	info.APIKey = m.APIKeyEnv // separat gesetzt, damit Redaction-Filter das Feld nicht als Secret-Assignment maskiert
	return info
}

// modelInfoToEngine ist die Umkehrung von modelInfoFromEngine
// (für den CSV-Export über sigoengine.WriteModelsCSV).
func modelInfoToEngine(id string, info ModelInfo) sigoengine.Model {
	return sigoengine.Model{
		ID:                       id,
		Shortcode:                info.Shortcode,
		Endpoint:                 info.Endpoint,
		APIKeyEnv:                info.APIKey,
		MaxInputTokens:           info.MaxInputTokens,
		MaxOutputTokens:          info.MaxOutputTokens,
		InputCost:                info.InputCost,
		OutputCost:               info.OutputCost,
		CachedInputCost:          info.CachedInputCost,
		MinTemperature:           info.MinTemperature,
		MaxTemperature:           info.MaxTemperature,
		RequiresCompletionTokens: info.RequiresCompletionTokens,
		UpstreamID:               info.UpstreamID,
	}
}

// loadModelsFromProviders ruft alle Provider-APIs beim Start ab.
// Fehler bei einzelnen Providern werden geloggt; der Server startet
// trotzdem mit den verfügbaren Modellen.
//
// reg synchronisiert nach jedem ERFOLGREICHEN Fetch die persistente
// ID-Registry (sigoengine.IDRegistry) und liefert den einmalig
// vergebenen, über Boots hinweg stabilen Shortcode statt des bisherigen
// pro-Boot berechneten. reg darf nil sein (Registry konnte nicht
// geöffnet werden) — Shortcodes werden dann wie bisher pro Boot neu
// berechnet, nil-safe analog zu costDB.
func loadModelsFromProviders(reg *sigoengine.IDRegistry) map[string]ModelInfo {
	models := make(map[string]ModelInfo)

	// Retry-Parameter: 4 Versuche mit 2s/4s/8s Backoff. Fängt den Fall ab,
	// dass beim Systemstart DNS noch nicht verfügbar ist (siehe FetchWithRetry).
	const fetchAttempts = 4
	const fetchBackoff = 2 * time.Second

	fetchers := []struct {
		provider string
		fn       func() ([]sigoengine.Model, error)
	}{
		{"mammouth", sigoengine.FetchMammouthModels},
		{"moonshot", sigoengine.FetchMoonshotModels},
		{"zai", sigoengine.FetchZAIModels},
		{"longcat", sigoengine.FetchLongcatModels},
		{"cheaperinference", sigoengine.FetchCheaperinferenceModels},
	}

	for _, f := range fetchers {
		ms, err := sigoengine.FetchWithRetry(f.provider, fetchAttempts, fetchBackoff, f.fn)
		if err != nil {
			sigoengine.LogWarn(f.provider+"-Modelle nicht geladen", map[string]interface{}{"error": err.Error()})
			continue
		}

		var entries map[string]sigoengine.ModelEntry
		if reg != nil {
			// Der Fetcher hat den semantischen Code bereits mit einer über
			// den ganzen Fetch mitlaufenden used-Map berechnet — die
			// Registry übernimmt ihn als Hint, statt ihn pro ID blind neu
			// zu berechnen (das lieferte bei cheaperinference/Longcat für
			// alle Modelle denselben Cutter-Code). Das "ci-"-Präfix von
			// cheaperinference wird abgeschnitten: die Registry stellt dem
			// Code ohnehin ihr 3-Zeichen-Provider-Kürzel voran ("che-"),
			// sonst entstünde doppelt markiertes "che-ci-cl-o51".
			// TrimPrefix ist für alle anderen Provider ein No-Op.
			seeds := make([]sigoengine.ProviderModelSeed, len(ms))
			for i, m := range ms {
				seeds[i] = sigoengine.ProviderModelSeed{
					UpstreamID:   m.ID,
					SemanticHint: strings.TrimPrefix(m.Shortcode, "ci-"),
				}
			}
			entries, err = reg.SyncProvider(f.provider, seeds)
			if err != nil {
				sigoengine.LogWarn("ID-Registry-Sync fehlgeschlagen", map[string]interface{}{
					"provider": f.provider, "error": err.Error(),
				})
			}
		}

		for _, m := range ms {
			info := modelInfoFromEngine(m)
			if entry, ok := entries[m.ID]; ok {
				info.Shortcode = entry.Shortcode
			}
			models[m.ID] = info
		}
	}

	sigoengine.LogInfo("Provider-Modelle geladen", map[string]interface{}{"count": len(models)})
	return models
}

// **********************************************************************
// Memory-Block laden

// loadMemory liest memory.json aus dem Datenverzeichnis (Disk hat Vorrang vor embedded)
func loadMemory(dataDir string) sigoengine.MemoryBlock {
	var jsonContent []byte

	path := filepath.Join(dataDir, "memory.json")
	data, err := os.ReadFile(path)
	if err == nil {
		jsonContent = data
		sigoengine.LogInfo("memory.json von Disk geladen", map[string]interface{}{"path": path})
	} else {
		jsonContent = []byte(defaultMemoryJSON)
		sigoengine.LogInfo("memory.json (embedded default) verwendet")
	}

	var mem sigoengine.MemoryBlock
	if err := json.Unmarshal(jsonContent, &mem); err != nil {
		sigoengine.LogWarn("memory.json Parse-Fehler, verwende leer", map[string]interface{}{"error": err.Error()})
	}
	return mem
}

// loadSystemPrompt liest system-prompt.txt aus dem Datenverzeichnis (optional).
// Gibt leeren String zurück wenn Datei nicht existiert.
func loadSystemPrompt(dataDir string) string {
	path := filepath.Join(dataDir, "system-prompt.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// **********************************************************************
// Request/Response Typen (OpenAI-kompatibel)

type ChatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type ChatRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	Temp           float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens"`
	SessionID      string          `json:"session_id"`                // sigoREST-Erweiterung
	Timeout        int             `json:"timeout"`                   // sigoREST-Erweiterung
	Retries        int             `json:"retries"`                   // sigoREST-Erweiterung
	SystemPrompt   string          `json:"system_prompt"`             // per-Request Override
	Bare           bool            `json:"bare"`                      // sigoREST-Erweiterung: kein Memory, kein Server-System-Prompt
	Channel        string          `json:"channel"`                   // optionaler Kanal, z.B. "mammouth-0"
	Stream         bool            `json:"stream"`                    // OpenAI streaming flag (new)
	ResponseFormat json.RawMessage `json:"response_format,omitempty"` // OpenAI-Feld ({"type":"text"|"json_object"|"json_schema",...}); 1:1 an den Provider durchgereicht
}

type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

type ChatUsage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
	// CostUSD ist nil (→ JSON null), wenn für das Modell kein Preis bekannt
	// ist (Fetcher hat keine Preisdaten geliefert) — nicht 0, das würde wie
	// "gratis" statt "unbekannt" aussehen (golisp2-Anlass, TODO.md 20261003).
	// Bei bekanntem Preis: ohne Cache-Rabatt obere Schranke bei OpenAI-
	// kompatiblen Providern ohne cheaperinference-Preisliste.
	CostUSD *float64 `json:"cost_usd"`
}

type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// buildChatUsage übersetzt sigoengine.UsageData in die Response-Form
// (inkl. cost_usd aus den Modell-Preisen). Gemeinsam genutzt von der
// Non-Streaming-Antwort (handleChatCompletions) und dem synthetischen
// Usage-Chunk, den streamProviderResponse vor [DONE] einschiebt.
func buildChatUsage(u *sigoengine.UsageData, inputCostPerM, outputCostPerM, cachedInputCostPerM float64, priceKnown bool) *ChatUsage {
	cu := &ChatUsage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.CachedTokens > 0 {
		cu.PromptTokensDetails = &PromptTokensDetails{CachedTokens: u.CachedTokens}
	}
	if u.ReasoningTokens > 0 {
		cu.CompletionTokensDetails = &CompletionTokensDetails{ReasoningTokens: u.ReasoningTokens}
	}
	if priceKnown {
		_, _, total := sigoengine.CalcCostUSD(
			int64(u.InputTokens), int64(u.OutputTokens), int64(u.CachedTokens),
			inputCostPerM, outputCostPerM, cachedInputCostPerM,
		)
		cu.CostUSD = &total
	}
	return cu
}

type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   *ChatUsage   `json:"usage,omitempty"`
}

type ErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// lookupResult ist das Ergebnis einer lookupModel-Auflösung.
// Bei Retired == true ist Info ggf. leer (das Modell ist nicht mehr in
// s.models, weil es aus der Live-Provider-Liste verschwunden ist) —
// Aufrufer müssen Retired vor Info prüfen.
type lookupResult struct {
	Info      ModelInfo
	ID        string
	Channel   string // "" = Default-Kanal, sonst Kanal-Override aus Shortcode-Suffix
	Retired   bool
	RetiredAt time.Time
}

// lookupModelMemory sucht case-insensitiv nach ID (Map-Key) oder
// Shortcode — ausschließlich in s.models, ohne Registry-Zugriff.
// Aufrufer muss s.mu halten (Lock oder RLock). Provider-Modell-IDs sind
// überwiegend lowercase; Sigil/CLI können aber andere Casing mitschicken
// (z.B. "GLM-4.5"), die ansonsten am exakten Map-Key-Lookup scheitern
// würden.
//
// Reihenfolge: (1) exakte ID, (2) exakter Shortcode — beide decken den
// aktiven Default-Kanal-Fall ab.
func (s *Server) lookupModelMemory(query string) (lookupResult, bool) {
	q := strings.ToLower(query)
	for id, info := range s.models {
		if strings.ToLower(id) == q {
			return lookupResult{Info: info, ID: id}, true
		}
	}
	for _, info := range s.models {
		if strings.ToLower(info.Shortcode) == q {
			return lookupResult{Info: info, ID: info.ID}, true
		}
	}
	return lookupResult{}, false
}

// lookupModel ist die vollständige Auflösung: erst der In-Memory-Teil
// (lookupModelMemory), dann die persistente ID-Registry, die zusätzlich
// Shortcodes mit Kanal-Suffix ("zai-glm45-2") und retired Modelle
// auflöst, welche per Definition nicht mehr in s.models stehen.
//
// Diese Funktion nimmt s.mu SELBST (RLock) — Aufrufer dürfen den Lock
// NICHT halten. Grund: die Registry-Abfrage geht auf Platte (SQLite mit
// busy_timeout(5000)); würde sie unter dem server-weiten RWMutex laufen,
// könnte ein langsamer DB-Zugriff bis zu 5s lang alle Reader blockieren
// (Go's RWMutex lässt neue Reader hinter einem wartenden Writer
// verhungern). Aufrufer, die den Lock bereits halten, nutzen
// lookupModelMemory.
func (s *Server) lookupModel(query string) (lookupResult, bool) {
	s.mu.RLock()
	if lr, ok := s.lookupModelMemory(query); ok {
		s.mu.RUnlock()
		return lr, true
	}
	reg := s.idRegistry
	s.mu.RUnlock()

	if reg == nil {
		return lookupResult{}, false
	}
	entry, channel, err := reg.ResolveShortcode(strings.ToLower(query))
	if err != nil {
		return lookupResult{}, false
	}
	res := lookupResult{ID: entry.UpstreamID, Channel: channel}
	if entry.RetiredAt != nil {
		res.Retired = true
		res.RetiredAt = *entry.RetiredAt
		return res, true
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, info := range s.models {
		if strings.ToLower(info.Shortcode) == entry.Shortcode {
			res.Info = info
			res.ID = id
			return res, true
		}
	}
	// Registry kennt den Shortcode, aber das Modell ist aktuell nicht in
	// s.models (z.B. Race zwischen Registry-Sync und einem sehr kurzen
	// Fetch-Ausfall) — als nicht gefunden behandeln statt mit leerer
	// ModelInfo weiterzumachen.
	return lookupResult{}, false
}

// providerForModel returns the provider name for a given model ID/shortcode.
// Delegiert an sigoengine.ResolveProvider (einzige kanonische Quelle,
// geteilt mit sigoE-CLI und Kosten-Tracking).
// Nimmt KEINEN Lock: lookupModel sperrt selbst (siehe dort).
func (s *Server) providerForModel(modelID string) string {
	lr, ok := s.lookupModel(modelID)
	endpoint := ""
	if ok {
		endpoint = lr.Info.Endpoint
	}
	return sigoengine.ResolveProvider(endpoint, modelID)
}

// providerForModelLocked ist die lock-freie Variante für Aufrufer, die
// s.mu bereits halten (z.B. handleModels/handleShortlist unter
// "defer s.mu.RUnlock()") — vermeidet einen rekursiven RLock, den
// Go's RWMutex nicht sicher garantiert (Deadlock-Risiko, wenn ein
// Writer dazwischen wartet). Nutzt deshalb lookupModelMemory und nicht
// das selbst sperrende lookupModel. Das ist für diesen Pfad auch
// inhaltlich richtig: hier werden ausschließlich bereits bekannte
// Modelle aus s.models angezeigt, nie Kanal-Suffixe oder retired
// Einträge aufgelöst.
func (s *Server) providerForModelLocked(modelID string) string {
	lr, ok := s.lookupModelMemory(modelID)
	endpoint := ""
	if ok {
		endpoint = lr.Info.Endpoint
	}
	return sigoengine.ResolveProvider(endpoint, modelID)
}

// recordUsage aktualisiert die Token-Statistiken für ein Modell und den
// tatsächlich genutzten Kanal (RAM, seit Serverstart) und schreibt zusätzlich
// ein persistentes Kosten-Event nach costs.db (sessionID optional, leer wenn
// keine Session verwendet wurde). Gemeinsam genutzt von /v1/chat/completions
// und /v1/messages.
func (s *Server) recordUsage(modelID string, ch *sigoengine.Channel, usage *sigoengine.UsageData) {
	s.recordUsageWithSession(modelID, ch, usage, "")
}

func (s *Server) recordUsageWithSession(modelID string, ch *sigoengine.Channel, usage *sigoengine.UsageData, sessionID string) {
	s.usageMu.Lock()

	stats, ok := s.usage[modelID]
	if !ok {
		stats = &ModelUsageStats{}
		s.usage[modelID] = stats
	}
	stats.InputTokens += int64(usage.InputTokens)
	stats.OutputTokens += int64(usage.OutputTokens)
	stats.TotalTokens += int64(usage.TotalTokens)
	stats.Requests++

	channelKey := fmt.Sprintf("%s#%s", modelID, ch.FullName())
	channelStats, ok := s.usageByChannel[channelKey]
	if !ok {
		channelStats = &ModelUsageStats{}
		s.usageByChannel[channelKey] = channelStats
	}
	channelStats.InputTokens += int64(usage.InputTokens)
	channelStats.OutputTokens += int64(usage.OutputTokens)
	channelStats.TotalTokens += int64(usage.TotalTokens)
	channelStats.Requests++
	s.usageMu.Unlock()

	// Persistentes Kosten-Tracking (costs.db). Nie den Response-Pfad
	// blockieren oder scheitern lassen — nur loggen.
	if s.costDB == nil {
		return
	}
	s.mu.RLock()
	info, exists := s.models[modelID]
	s.mu.RUnlock()
	var inCost, outCost, total float64
	if exists {
		inCost, outCost, total = sigoengine.CalcCostUSD(
			int64(usage.InputTokens), int64(usage.OutputTokens), int64(usage.CachedTokens),
			info.InputCost, info.OutputCost, info.CachedInputCost,
		)
	}
	provider := s.providerForModel(modelID)
	err := s.costDB.RecordUsage(sigoengine.UsageEvent{
		Model:         modelID,
		Provider:      provider,
		Channel:       ch.FullName(),
		SessionID:     sessionID,
		InputTokens:   int64(usage.InputTokens),
		OutputTokens:  int64(usage.OutputTokens),
		TotalTokens:   int64(usage.TotalTokens),
		InputCostUSD:  inCost,
		OutputCostUSD: outCost,
		TotalCostUSD:  total,
	})
	if err != nil {
		sigoengine.LogWarn("Kosten-Event konnte nicht gespeichert werden", map[string]interface{}{
			"model": modelID, "error": err.Error(),
		})
	}
}

// **********************************************************************
// HTTP Handler

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "Method not allowed", "invalid_request", http.StatusMethodNotAllowed)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid JSON: "+err.Error(), "invalid_request", http.StatusBadRequest)
		return
	}

	// Modell-Validierung (ID oder Shortcode, case-insensitiv)
	modelID := req.Model

	// Case-insensitiv nach ID oder Shortcode suchen. lookupModel sperrt
	// selbst (kann bis in die SQLite-Registry laufen) — hier darf s.mu
	// deshalb NICHT gehalten werden.
	lr, exists := s.lookupModel(modelID)
	if exists {
		modelID = lr.ID
	}

	s.mu.RLock()
	mem := s.memory
	globalSystemPrompt := s.systemPrompt
	s.mu.RUnlock()

	if !exists {
		writeError(w, fmt.Sprintf("Model '%s' nicht gefunden", req.Model), "model_not_found", http.StatusBadRequest)
		return
	}
	if lr.Retired {
		writeError(w, fmt.Sprintf(
			"Modell '%s' ist seit %s nicht mehr verfügbar. Kein automatischer Fallback auf ein anderes Modell.",
			modelID, lr.RetiredAt.Format("2006-01-02"),
		), "model_retired", http.StatusGone)
		return
	}
	modelInfo := lr.Info
	if lr.Channel != "" && req.Channel == "" {
		req.Channel = lr.Channel
	}

	// Streaming-Modus erkennen (OpenAI-Standard)
	isStreaming := req.Stream

	// Provider und Kanal bestimmen
	provider := s.providerForModel(modelID)
	ch, err := s.channelManager.Resolve(provider, req.Channel)
	if err != nil {
		apiErr := sigoengine.ClassifyError(err)
		httpStatus := http.StatusBadRequest
		if apiErr.Type == sigoengine.ErrConfigNotFound {
			httpStatus = http.StatusNotFound
		}
		writeError(w, err.Error(), apiErr.Type, httpStatus)
		return
	}

	// Config mit Kanal-Key aufbauen
	cfg, err := sigoengine.LoadConfigWithChannel(modelID, ch)
	if err != nil {
		writeError(w, err.Error(), "config_error", http.StatusInternalServerError)
		return
	}
	cfg.Endpoint = modelInfo.Endpoint
	if modelInfo.UpstreamID != "" {
		cfg.Model = modelInfo.UpstreamID
	}

	// Provider-Ping: scheitert → sofortiger Fehler, kein API-Call
	if err := sigoengine.PingProvider(modelInfo.Endpoint); err != nil {
		sigoengine.LogWarn("Provider nicht erreichbar", map[string]interface{}{
			"model":    modelID,
			"endpoint": modelInfo.Endpoint,
			"error":    err.Error(),
		})
		writeError(w, "Provider nicht erreichbar: "+err.Error(), "provider_unavailable", http.StatusServiceUnavailable)
		return
	}

	// Budget-Check: nur bei aktiviertem Hard-Stop und überschrittenem Limit
	// wird der Call abgelehnt — reines Tracking blockiert nie.
	if s.costDB != nil {
		if status, err := s.costDB.CheckBudget(time.Now()); err != nil {
			sigoengine.LogWarn("Budget-Check fehlgeschlagen", map[string]interface{}{"error": err.Error()})
		} else if status.Blocked {
			writeError(w, fmt.Sprintf(
				"Budget überschritten (Tag: $%.2f/$%.2f, Monat: $%.2f/$%.2f) — Hard-Stop aktiv",
				status.DailySpendUSD, status.DailyLimitUSD, status.MonthlySpendUSD, status.MonthlyLimitUSD,
			), "budget_exceeded", http.StatusPaymentRequired)
			return
		}
	}

	// Defaults setzen
	if req.MaxTokens == 0 && modelInfo.MaxOutputTokens > 0 {
		req.MaxTokens = modelInfo.MaxOutputTokens
	}
	// Temperatur festlegen:
	//   - Fixed-Temp-Modelle (Min==Max, z.B. kimi-k2.5 thinking): Wert immer
	//     erzwingen, User-Override ignorieren — sonst 400 vom Provider.
	//   - Sonst: User-Wert oder Default-Mittelpunkt, geclampt auf [Min,Max].
	switch {
	case modelInfo.MinTemperature == modelInfo.MaxTemperature:
		req.Temp = modelInfo.MinTemperature
	case req.Temp == 0:
		req.Temp = (modelInfo.MinTemperature + modelInfo.MaxTemperature) / 2.0
	default:
		if req.Temp < modelInfo.MinTemperature {
			req.Temp = modelInfo.MinTemperature
		} else if req.Temp > modelInfo.MaxTemperature {
			req.Temp = modelInfo.MaxTemperature
		}
	}
	if req.Timeout == 0 {
		req.Timeout = sigoengine.DEFAULT_TIMEOUT
	}
	if req.Retries == 0 {
		req.Retries = 3
	}

	// Messages aufbauen: Memory zuerst, dann user-Messages
	messages := []map[string]interface{}{}

	// Memory und Server-System-Prompt nur ohne bare. Mit bare bestimmt allein
	// der Client den Kontext: einzig ein nicht-leeres req.SystemPrompt.
	effectiveSystemPrompt := ""
	if !req.Bare {
		// Globaler Memory-Block als System-Message (immer zuerst)
		if mem.Content != "" {
			messages = append(messages, map[string]interface{}{
				"role":    "system",
				"content": mem.Content,
			})
		}

		// Kanal-spezifischer Memory-Block
		channelMemPath := sigoengine.ChannelMemoryPath(s.baseDir, ch.Provider, ch.Name)
		if data, err := os.ReadFile(channelMemPath); err == nil {
			var channelMem sigoengine.MemoryBlock
			if err := json.Unmarshal(data, &channelMem); err == nil && channelMem.Content != "" {
				messages = append(messages, map[string]interface{}{
					"role":    "system",
					"content": channelMem.Content,
				})
			}
		}

		// System-Prompt: Kanal vor globalem Default
		effectiveSystemPrompt = globalSystemPrompt
		channelPromptPath := sigoengine.ChannelSystemPromptPath(s.baseDir, ch.Provider, ch.Name)
		if data, err := os.ReadFile(channelPromptPath); err == nil {
			if prompt := strings.TrimSpace(string(data)); prompt != "" {
				effectiveSystemPrompt = prompt
			}
		}
	}
	// Request-Wert hat immer Vorrang
	if req.SystemPrompt != "" {
		effectiveSystemPrompt = req.SystemPrompt
	}
	if effectiveSystemPrompt != "" {
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": effectiveSystemPrompt,
		})
	}

	// Session-History laden und einbauen (wird bei Erfolg unter dem tatsächlich
	// genutzten Kanal gespeichert, damit Failover die Session nicht splittet).
	var session *sigoengine.Session
	if req.SessionID != "" {
		session = sigoengine.LoadSessionForChannel(s.baseDir, ch.Provider, ch.Name, req.SessionID, req.Model)
		for _, m := range session.History {
			messages = append(messages, map[string]interface{}{
				"role": m.Role, "content": m.Content,
			})
		}
	}

	// User-Messages aus Request
	var userPrompt string
	for _, msg := range req.Messages {
		var contentValue interface{}
		if err := json.Unmarshal(msg.Content, &contentValue); err != nil {
			contentValue = string(msg.Content)
		}
		if msg.Role == "system" {
			if req.SystemPrompt != "" {
				sigoengine.LogWarn("Ignoriere role:system in Messages, da system_prompt im Request gesetzt ist", map[string]interface{}{
					"model": req.Model,
				})
				continue
			}
			messages = append(messages, map[string]interface{}{
				"role": "system", "content": contentValue,
			})
		} else {
			messages = append(messages, map[string]interface{}{
				"role": msg.Role, "content": contentValue,
			})
			if msg.Role == "user" {
				userPrompt = sigoengine.ExtractTextFromContent(msg.Content)
			}
		}
	}

	// priceKnown: true nur wenn ein Preis tatsächlich bekannt ist (>0) oder
	// das Modell bekannt kostenlos ist (Ollama, lokale Inferenz). Alle
	// anderen 0/0-Modelle sind "Preis unbekannt" (Fetcher hat keine Preisdaten
	// geliefert), nicht "gratis" — cost_usd wird dafür null statt 0 (TODO.md
	// 20261003, golisp2-Anlass).
	priceKnown := modelInfo.InputCost > 0 || modelInfo.OutputCost > 0 || provider == "ollama"

	// API-Request aufbauen
	apiRequest := map[string]interface{}{
		"model":       cfg.Model,
		"messages":    messages,
		"temperature": req.Temp,
	}
	// max_tokens nur setzen wenn > 0 (0 → Provider-Default, verhindert leere Antworten)
	if req.MaxTokens > 0 {
		apiRequest["max_tokens"] = req.MaxTokens
	}
	// GPT-5: max_completion_tokens statt max_tokens
	if modelInfo.RequiresCompletionTokens {
		delete(apiRequest, "max_tokens")
		apiRequest["max_completion_tokens"] = req.MaxTokens
	}
	// response_format 1:1 durchreichen (golisp2 braucht JSON-Modus zuverlässig,
	// sonst verpacken Modelle JSON gern in einen Markdown-Codeblock). Alle
	// über diesen Endpoint erreichbaren Provider sind OpenAI-kompatibel
	// (cfg.Type ist hier immer "mammoth"/"ollama", nie "anthropic" —
	// LoadConfigWithChannel kennt kein natives Anthropic-Channel-Type) — eine
	// Sonderbehandlung für einen Anthropic-Pfad entfällt deshalb.
	if len(req.ResponseFormat) > 0 {
		apiRequest["response_format"] = req.ResponseFormat
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.Timeout)*time.Second)
	defer cancel()

	var responseText string
	var responseUsage *sigoengine.UsageData
	var responseFinishReason string
	var successfulCh *sigoengine.Channel

	// Input-Text fur Fallback-Schatzung sammeln
	var inputBuilder strings.Builder
	for _, msg := range req.Messages {
		inputBuilder.WriteString(sigoengine.ExtractTextFromContent(msg.Content))
	}
	inputText := inputBuilder.String()

	// Liste der zu probierenden Kanäle aufbauen (initial + Failover)
	channelsToTry := s.channelManager.FailoverList(provider, ch)

	// Exponential Backoff Retry
	retryConfig := sigoengine.DefaultRetryConfig()
	retryConfig.MaxRetries = req.Retries

	var lastErr error
	var streamed bool
	// streamStarted: sobald streamProviderResponse aufgerufen wurde, hat es
	// bereits w.WriteHeader(200) geschrieben (erste Anweisung der Funktion),
	// unabhängig davon ob sie am Ende erfolgreich zurückkehrt. Ein späterer
	// Fehler darf dann weder einen zweiten Stream-Preamble auf denselben
	// ResponseWriter schreiben (Failover auf nächsten Kanal) noch eine JSON-
	// Fehlerantwort auf den bereits offenen text/event-stream-Body glueen.
	var streamStarted bool
	for _, currentCh := range channelsToTry {
		cfg, err := sigoengine.LoadConfigWithChannel(modelID, currentCh)
		if err != nil {
			lastErr = err
			continue
		}
		cfg.Endpoint = modelInfo.Endpoint
		if modelInfo.UpstreamID != "" {
			cfg.Model = modelInfo.UpstreamID
		}

		// Rate-Limiter pro Kanal (hybrid): wartet bis minInterval seit
		// letztem Call vergangen, spätestens nach maxWait → ErrRateLimited
		// → Failover auf nächsten Kanal (oder HTTP 429 am Ende).
		minInt := s.rateMinInterval
		if currentCh.MinInterval > 0 {
			minInt = time.Duration(currentCh.MinInterval) * time.Millisecond
		}
		maxW := s.rateMaxWait
		if currentCh.MaxWait > 0 {
			maxW = time.Duration(currentCh.MaxWait) * time.Millisecond
		}
		if minInt > 0 {
			if err := s.rateLimiter.Acquire(ctx, currentCh.FullName(), minInt, maxW); err != nil {
				if err == sigoengine.ErrRateLimited {
					sigoengine.LogWarn("Rate-Limit: Kanal überlastet, Failover", map[string]interface{}{
						"channel":     currentCh.FullName(),
						"max_wait_ms": maxW.Milliseconds(),
					})
					lastErr = err
					continue
				}
				// ctx abgebrochen oder anderer Fehler
				lastErr = err
				break
			}
			s.rateLimiter.Release(currentCh.FullName())
		}

		// Circuit Breaker pro Kanal (Key: model#channel)
		cbKey := fmt.Sprintf("%s#%s", req.Model, currentCh.FullName())
		s.mu.Lock()
		if _, exists := s.breakers[cbKey]; !exists {
			config := &sigoengine.CircuitBreakerConfig{
				Threshold:   5,
				Window:      60 * time.Second,
				Cooldown:    10 * time.Second,
				HalfOpenMax: 3,
			}
			s.breakers[cbKey] = sigoengine.NewEnhancedCircuitBreaker(config)
		}
		breaker := s.breakers[cbKey]
		s.mu.Unlock()

		// Echtes Streaming nur für OpenAI-kompatible Provider.
		// Anthropic wird als normale JSON-Antwort behandelt (kein Fake-Streaming).
		if isStreaming && cfg.Type != "anthropic" {
			lastErr = breaker.Do(func() error {
				stream, e := sigoengine.CallAPIStream(ctx, cfg, apiRequest)
				if e != nil {
					return e
				}
				// Ab hier hat streamProviderResponse garantiert bereits
				// WriteHeader(200) aufgerufen (erste Anweisung der Funktion) —
				// egal ob sie am Ende erfolgreich zurückkehrt oder nicht.
				streamStarted = true
				text, u, e := s.streamProviderResponse(w, stream, req.Model, modelInfo.InputCost, modelInfo.OutputCost, modelInfo.CachedInputCost, priceKnown)
				if e != nil {
					return e
				}
				responseText = text
				responseUsage = u
				streamed = true
				return nil
			})
		} else {
			lastErr = sigoengine.RetryWithBackoff(ctx, retryConfig, func() error {
				return breaker.Do(func() error {
					text, u, fr, _, e := sigoengine.CallAPI(ctx, cfg, apiRequest, req.Timeout)
					if e != nil {
						apiErr := sigoengine.ClassifyError(e)
						if apiErr.Type == sigoengine.ErrAuthFailed {
							if err := s.channelManager.Registry().SetActive(currentCh.Provider, currentCh.Name, false); err != nil {
								sigoengine.LogWarn("Konnte Kanal nach Auth-Fehler nicht deaktivieren", map[string]interface{}{
									"provider": currentCh.Provider,
									"channel":  currentCh.Name,
									"error":    err.Error(),
								})
							}
						}
						return e
					}
					responseText = text
					responseUsage = u
					responseFinishReason = fr
					return nil
				})
			})
		}

		if lastErr == nil {
			successfulCh = currentCh
			// Lazy Health: erfolgreicher User-Request → Kanal healthy
			s.channelManager.Registry().MarkChannelHealth(currentCh.Provider, currentCh.Name, true, "")
			break
		}

		// Lazy Health: fehlgeschlagener User-Request → Kanal unhealthy
		s.channelManager.Registry().MarkChannelHealth(currentCh.Provider, currentCh.Name, false, lastErr.Error())

		apiErr := sigoengine.ClassifyError(lastErr)
		if streamStarted || apiErr.Type == sigoengine.ErrClientError {
			// streamStarted: Client hat bereits einen halb-offenen Stream —
			// ein Failover auf den nächsten Kanal würde einen zweiten
			// Stream-Preamble auf denselben ResponseWriter schreiben.
			break
		}
		sigoengine.LogWarn("Failing over to next channel", map[string]interface{}{
			"model":      req.Model,
			"channel":    currentCh.FullName(),
			"error_type": apiErr.Type,
		})
	}

	if lastErr != nil && streamStarted {
		// streamProviderResponse hat den Stream bereits bestmöglich sauber
		// geschlossen (data: [DONE]-Terminator, siehe dort). Eine JSON-
		// Fehlerantwort auf den offenen text/event-stream-Body wäre für den
		// Client nicht parsebar — hier nur noch loggen, kein zweiter
		// Body-Write, Verbindung wird beendet.
		sigoengine.LogError("Stream-Fehler nach Header-Write, Verbindung wird beendet", lastErr, map[string]interface{}{
			"model": req.Model,
		})
		return
	}

	if lastErr != nil {
		// Eigener Rate-Limiter-Fehler (sentinel, kein APIError):
		// alle Kanäle waren innerhalb maxWait nicht frei → HTTP 429.
		if lastErr == sigoengine.ErrRateLimited {
			retryAfter := s.rateMaxWait.Seconds()
			if retryAfter < 1 {
				retryAfter = 1
			}
			sigoengine.LogWarn("Alle Kanäle rate-limitiert", map[string]interface{}{
				"model":       req.Model,
				"retry_after": retryAfter,
			})
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter))
			writeError(w, "rate limit exceeded: all channels throttled", "rate_limit", http.StatusTooManyRequests)
			return
		}

		// Fehler klassifizieren für typisierte Antwort
		apiErr := sigoengine.ClassifyError(lastErr)

		sigoengine.LogError("API-Call fehlgeschlagen", lastErr, map[string]interface{}{
			"model":       req.Model,
			"error_type":  apiErr.Type,
			"status_code": apiErr.StatusCode,
		})

		// HTTP-Status und Error-Type basierend auf Fehlerklasse
		httpStatus := http.StatusBadGateway
		errType := "api_error"

		switch apiErr.Type {
		case sigoengine.ErrRateLimit:
			httpStatus = http.StatusTooManyRequests // 429
			errType = "rate_limit"
			// Retry-After Header setzen
			if apiErr.RetryAfter > 0 {
				w.Header().Set("Retry-After", fmt.Sprintf("%.0f", apiErr.RetryAfter.Seconds()))
			}
		case sigoengine.ErrAuthFailed:
			httpStatus = http.StatusUnauthorized // 401
			errType = "auth_failed"
		case sigoengine.ErrTimeout:
			httpStatus = http.StatusGatewayTimeout // 504
			errType = "timeout"
		case sigoengine.ErrServerError:
			httpStatus = http.StatusServiceUnavailable // 503
			errType = "server_error"
		case sigoengine.ErrClientError:
			httpStatus = http.StatusBadRequest // 400
			errType = "client_error"
		case sigoengine.ErrCircuitOpen:
			httpStatus = http.StatusServiceUnavailable // 503
			errType = "circuit_open"
		}

		writeError(w, apiErr.Message, errType, httpStatus)
		return
	}

	// Usage schätzen falls Provider keine liefert
	if responseUsage == nil {
		responseUsage = sigoengine.EstimateUsage(inputText, responseText)
	}

	// Session speichern (unter dem Kanal, der tatsächlich geantwortet hat)
	if req.SessionID != "" && userPrompt != "" && successfulCh != nil {
		session.AddMessage("user", userPrompt)
		session.AddMessage("assistant", responseText)
		session.SaveForChannel(s.baseDir, successfulCh.Provider, successfulCh.Name, req.SessionID, req.Model)
	}

	// Usage akkumulieren
	chatUsage := buildChatUsage(responseUsage, modelInfo.InputCost, modelInfo.OutputCost, modelInfo.CachedInputCost, priceKnown)
	s.recordUsageWithSession(modelID, successfulCh, responseUsage, req.SessionID)

	// Bei echtem Streaming wurde die Antwort bereits geschrieben.
	if streamed {
		return
	}

	// OpenAI-kompatible JSON-Antwort (non-streaming oder Anthropic-streaming)
	resp := ChatResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []ChatChoice{{
			Index:        0,
			Message:      ChatMessage{Role: "assistant", Content: json.RawMessage(`"` + jsonEscapeString(responseText) + `"`)},
			FinishReason: responseFinishReason,
		}},
		Usage: chatUsage,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// **********************************************************************
// POST /v1/embeddings - OpenAI-kompatible Embedding-API (Proxy auf Ollama)
//
// Unterstützt ausschließlich lokale Ollama-Modelle (via DiscoverOllamaModels
// bekannt). Externe Embedding-Provider sind bewusst nicht integriert (YAGNI).
// Kosten-Tracking entfällt: ollama = 0 USD, Budget-Check nicht erforderlich.

// EmbeddingRequest — OpenAI-kompatible Embedding-Anfrage.
// Input kann ein String oder ein Array von Strings sein.
type EmbeddingRequest struct {
	Model string      `json:"model"`
	Input interface{} `json:"input"`
}

// EmbeddingData — ein Embedding-Eintrag in der OpenAI-Response.
type EmbeddingData struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

// EmbeddingResponse — OpenAI-kompatible Embedding-Antwort.
type EmbeddingResponse struct {
	Object string          `json:"object"`
	Data   []EmbeddingData `json:"data"`
	Model  string          `json:"model"`
}

// handleEmbeddings verarbeitet POST /v1/embeddings.
// Löst das Modell über die bestehende Lookup-Infrastruktur auf, leitet die
// Anfrage an Ollama /api/embed weiter und mappt die Antwort ins OpenAI-Format.
func (s *Server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "Method not allowed", "invalid_request", http.StatusMethodNotAllowed)
		return
	}

	var req EmbeddingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid JSON: "+err.Error(), "invalid_request", http.StatusBadRequest)
		return
	}

	// Input normalisieren: String oder Array von Strings.
	// Leerer String oder leeres Array → 400.
	var inputs []string
	switch v := req.Input.(type) {
	case string:
		if v == "" {
			writeError(w, "input darf nicht leer sein", "invalid_request", http.StatusBadRequest)
			return
		}
		inputs = []string{v}
	case []interface{}:
		if len(v) == 0 {
			writeError(w, "input darf nicht leer sein", "invalid_request", http.StatusBadRequest)
			return
		}
		inputs = make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				writeError(w, "input-Array darf nur Strings enthalten", "invalid_request", http.StatusBadRequest)
				return
			}
			inputs = append(inputs, s)
		}
	case []string:
		if len(v) == 0 {
			writeError(w, "input darf nicht leer sein", "invalid_request", http.StatusBadRequest)
			return
		}
		inputs = v
	default:
		writeError(w, "input muss ein String oder ein Array von Strings sein", "invalid_request", http.StatusBadRequest)
		return
	}

	// Modell auflösen (ID oder Shortcode, wie bei Chat). lookupModel sperrt
	// selbst — s.mu darf hier NICHT gehalten werden.
	modelID := req.Model
	lr, exists := s.lookupModel(modelID)
	if exists {
		modelID = lr.ID
	}
	if !exists {
		writeError(w, fmt.Sprintf("Model '%s' nicht gefunden", req.Model), "model_not_found", http.StatusBadRequest)
		return
	}
	if lr.Retired {
		writeError(w, fmt.Sprintf(
			"Modell '%s' ist seit %s nicht mehr verfügbar. Kein automatischer Fallback auf ein anderes Modell.",
			modelID, lr.RetiredAt.Format("2006-01-02"),
		), "model_retired", http.StatusGone)
		return
	}

	// Ollama-Modell? Embedding derzeit nur für lokale ollama-Modelle.
	ollamaModels := sigoengine.GetOllamaModels()
	ollamaInfo, isOllama := ollamaModels[lr.Info.Shortcode]
	if !isOllama {
		// Fallback: bei Ollama-Modellen ist ID == Shortcode.
		ollamaInfo, isOllama = ollamaModels[lr.Info.ID]
	}
	if !isOllama {
		writeError(w, "Embedding derzeit nur für lokale ollama-Modelle unterstützt", "invalid_request", http.StatusBadRequest)
		return
	}

	// Ollama /api/embed aufrufen.
	// Body:   {"model": "<OllamaName>", "input": [<alle Eingabetexte>]}
	// Antwort: {"model": "...", "embeddings": [[...], [...]]}
	ollamaReq := map[string]interface{}{
		"model": ollamaInfo.OllamaName,
		"input": inputs,
	}
	body, err := json.Marshal(ollamaReq)
	if err != nil {
		writeError(w, "interner Fehler beim Serialisieren der Anfrage: "+err.Error(), "internal_error", http.StatusInternalServerError)
		return
	}
	client := &http.Client{Timeout: 60 * time.Second, Transport: sigoengine.NewCommLogTransport(nil)}
	resp, err := client.Post(ollamaEndpoint+"/api/embed", "application/json", bytes.NewReader(body))
	if err != nil {
		sigoengine.LogWarn("Ollama nicht erreichbar", map[string]interface{}{
			"model":    ollamaInfo.OllamaName,
			"endpoint": ollamaEndpoint,
			"error":    err.Error(),
		})
		writeError(w, "Provider nicht erreichbar: "+err.Error(), "provider_unavailable", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			sigoengine.LogWarn("Antwort von Ollama nicht lesbar", map[string]interface{}{
				"model": ollamaInfo.OllamaName,
				"error": err.Error(),
			})
			writeError(w, "Antwort des Providers nicht lesbar: "+err.Error(), "provider_error", http.StatusBadGateway)
			return
		}
		sigoengine.LogWarn("Ollama /api/embed Fehler", map[string]interface{}{
			"model":  ollamaInfo.OllamaName,
			"status": resp.StatusCode,
			"body":   string(respBody),
		})
		writeError(w, fmt.Sprintf("Ollama-Fehler (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody))), "api_error", http.StatusBadGateway)
		return
	}

	var ollamaResp struct {
		Model      string      `json:"model"`
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		writeError(w, "Ollama-Antwort nicht parsebar: "+err.Error(), "api_error", http.StatusBadGateway)
		return
	}

	// Anzahl Embeddings muss mit Input-Anzahl übereinstimmen.
	if len(ollamaResp.Embeddings) != len(inputs) {
		writeError(w, fmt.Sprintf(
			"Anzahl Embeddings (%d) entspricht nicht Input-Anzahl (%d)",
			len(ollamaResp.Embeddings), len(inputs),
		), "api_error", http.StatusBadGateway)
		return
	}

	// In OpenAI-Format mappen: ein data-Element pro input, index aufsteigend.
	data := make([]EmbeddingData, len(ollamaResp.Embeddings))
	for i, emb := range ollamaResp.Embeddings {
		data[i] = EmbeddingData{
			Object:    "embedding",
			Index:     i,
			Embedding: emb,
		}
	}

	embResp := EmbeddingResponse{
		Object: "list",
		Data:   data,
		Model:  modelID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(embResp)
}

// **********************************************************************
// GET /v1/models - OpenAI-kompatible Modell-Liste
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	type ModelData struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}

	var models []ModelData
	for id, info := range s.models {
		provider := s.providerForModelLocked(id)
		// ID und Shortcode hinzufügen
		models = append(models, ModelData{
			ID:      id,
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: provider,
		})
		if info.Shortcode != id {
			models = append(models, ModelData{
				ID:      info.Shortcode,
				Object:  "model",
				Created: time.Now().Unix(),
				OwnedBy: provider,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   models,
	})
}

// **********************************************************************
// GET /api/models - Volle Modell-Infos (JSON, oder ?format=csv)
func (s *Server) handleAPIModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	switch r.URL.Query().Get("format") {
	case "", "json":
	case "csv":
		s.writeModelsCSV(w)
		return
	default:
		writeError(w, "unsupported format (erlaubt: json, csv)", "invalid_request", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var models []ModelInfo
	for id, info := range s.models {
		provider := s.providerForModelLocked(id)
		mi := ModelInfo{
			ID:                       id,
			Shortcode:                info.Shortcode,
			Endpoint:                 info.Endpoint,
			MaxInputTokens:           info.MaxInputTokens,
			MaxOutputTokens:          info.MaxOutputTokens,
			InputCost:                info.InputCost,
			OutputCost:               info.OutputCost,
			CachedInputCost:          info.CachedInputCost,
			MinTemperature:           info.MinTemperature,
			MaxTemperature:           info.MaxTemperature,
			RequiresCompletionTokens: info.RequiresCompletionTokens,
			UpstreamID:               info.UpstreamID,
			Provider:                 provider,
			ProviderCode:             sigoengine.ProviderCode(provider),
		}
		mi.APIKey = info.APIKey // separat gesetzt, damit Redaction-Filter das Feld nicht als Secret-Assignment maskiert
		models = append(models, mi)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models)
}

// writeModelsCSV liefert die Live-Modellliste im Registry-CSV-Format
// (semikolon-getrennt, als CLI-models.csv wiederverwendbar). Snapshot unter
// RLock, Schreiben aufs Netz danach — ein langsamer Client blockiert so
// keine Writer auf s.mu.
func (s *Server) writeModelsCSV(w http.ResponseWriter) {
	s.mu.RLock()
	models := make([]sigoengine.Model, 0, len(s.models))
	for id, info := range s.models {
		models = append(models, modelInfoToEngine(id, info))
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="models.csv"`)
	if err := sigoengine.WriteModelsCSV(w, models); err != nil {
		sigoengine.LogWarn("CSV-Export der Modellliste fehlgeschlagen", map[string]interface{}{"error": err.Error()})
	}
}

// **********************************************************************
// GET /api/shortcodes - Modell → Shortcode Mapping
func (s *Server) handleShortcodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Nach ID sortieren für deterministische Ausgabe
	type scEntry struct {
		ID        string `json:"id"`
		Shortcode string `json:"sc"`
	}

	entries := make([]scEntry, 0, len(s.models))
	for _, info := range s.models {
		entries = append(entries, scEntry{ID: info.ID, Shortcode: info.Shortcode})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ID < entries[j].ID
	})

	// Kompaktes Format: {"id": "sc", ...}
	result := make(map[string]string, len(entries))
	for _, e := range entries {
		result[e.ID] = e.Shortcode
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// **********************************************************************
// GET /api/shortlist - kompakte Liste: nur Shortcode + Anbieter, ohne
// die ID/Shortcode-Dopplung von /v1/models.
func (s *Server) handleShortlist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	type shortEntry struct {
		Shortcode string `json:"shortcode"`
		Provider  string `json:"provider"`
		Code      string `json:"code"` // normierter 5-Zeichen-Provider-Code, z.B. "mammo", "zai__"
	}

	entries := make([]shortEntry, 0, len(s.models))
	for id, info := range s.models {
		provider := s.providerForModelLocked(id)
		entries = append(entries, shortEntry{
			Shortcode: info.Shortcode,
			Provider:  provider,
			Code:      sigoengine.ProviderCode(provider),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Provider != entries[j].Provider {
			return entries[i].Provider < entries[j].Provider
		}
		return entries[i].Shortcode < entries[j].Shortcode
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

// **********************************************************************
// GET /ping - Einfacher Health-Check für Load Balancer
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("pong"))
}

// GET /api/version - Versions-String
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"version":   sigoengine.Version,
		"component": "sigoREST",
	})
}

// **********************************************************************
// Channels API

// handleChannelRouter dispatches /api/channels/:provider/:name/:action
func (s *Server) handleChannelRouter(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	// /api/channels/<provider>/<name>/<action>
	if len(parts) < 4 {
		writeError(w, "Invalid channel path", "invalid_request", http.StatusBadRequest)
		return
	}
	provider := parts[2]
	name := parts[3]
	action := ""
	if len(parts) >= 5 {
		action = parts[4]
	}

	switch r.Method {
	case http.MethodGet:
		switch action {
		case "":
			s.handleChannelDetail(w, r, provider, name)
			return
		case "memory":
			s.handleChannelMemoryGet(w, r, provider, name)
			return
		case "system-prompt":
			s.handleChannelSystemPromptGet(w, r, provider, name)
			return
		}
	case http.MethodPut:
		switch action {
		case "memory":
			s.handleChannelMemoryPut(w, r, provider, name)
			return
		case "system-prompt":
			s.handleChannelSystemPromptPut(w, r, provider, name)
			return
		}
	case http.MethodPost:
		switch action {
		case "enable":
			s.handleChannelEnable(w, r, provider, name)
			return
		case "disable":
			s.handleChannelDisable(w, r, provider, name)
			return
		}
	}
	writeError(w, "Invalid channel operation", "invalid_request", http.StatusBadRequest)
}

// GET /api/channels - Liste aller Kanäle
func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.channelManager.AllChannelStatus())
}

// GET /api/channels/:provider/:name - Einzelkanal
func (s *Server) handleChannelDetail(w http.ResponseWriter, r *http.Request, provider, name string) {
	ch, ok := s.channelManager.Registry().GetChannel(provider, name)
	if !ok {
		writeError(w, "Channel not found", "not_found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"provider":           ch.Provider,
		"name":               ch.Name,
		"full_name":          ch.FullName(),
		"active":             ch.Active,
		"healthy":            ch.Healthy,
		"last_health_check":  ch.LastHealthCheck,
		"last_error":         ch.LastError,
		"consecutive_errors": ch.ConsecutiveErrors,
		"manually_disabled":  ch.ManuallyDisabled,
	})
}

// POST /api/channels/:provider/:name/enable
func (s *Server) handleChannelEnable(w http.ResponseWriter, r *http.Request, provider, name string) {
	if err := s.channelManager.Registry().SetActiveManual(provider, name, true); err != nil {
		writeError(w, err.Error(), "not_found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "enabled"})
}

// POST /api/channels/:provider/:name/disable
func (s *Server) handleChannelDisable(w http.ResponseWriter, r *http.Request, provider, name string) {
	if err := s.channelManager.Registry().SetActiveManual(provider, name, false); err != nil {
		writeError(w, err.Error(), "not_found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "disabled"})
}

func (s *Server) handleChannelMemoryGet(w http.ResponseWriter, r *http.Request, provider, name string) {
	path := sigoengine.ChannelMemoryPath(s.baseDir, provider, name)
	data, err := os.ReadFile(path)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sigoengine.MemoryBlock{})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (s *Server) handleChannelMemoryPut(w http.ResponseWriter, r *http.Request, provider, name string) {
	var mem sigoengine.MemoryBlock
	if err := json.NewDecoder(r.Body).Decode(&mem); err != nil {
		writeError(w, "Invalid JSON: "+err.Error(), "invalid_request", http.StatusBadRequest)
		return
	}
	path := sigoengine.ChannelMemoryPath(s.baseDir, provider, name)
	sigoengine.EnsureChannelDir(s.baseDir, provider, name)
	data, _ := json.MarshalIndent(mem, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		writeError(w, "Cannot write memory: "+err.Error(), "server_error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mem)
}

func (s *Server) handleChannelSystemPromptGet(w http.ResponseWriter, r *http.Request, provider, name string) {
	path := sigoengine.ChannelSystemPromptPath(s.baseDir, provider, name)
	data, err := os.ReadFile(path)
	prompt := ""
	if err == nil {
		prompt = strings.TrimSpace(string(data))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"system_prompt": prompt})
}

func (s *Server) handleChannelSystemPromptPut(w http.ResponseWriter, r *http.Request, provider, name string) {
	var body struct {
		SystemPrompt string `json:"system_prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, "Invalid JSON: "+err.Error(), "invalid_request", http.StatusBadRequest)
		return
	}
	path := sigoengine.ChannelSystemPromptPath(s.baseDir, provider, name)
	sigoengine.EnsureChannelDir(s.baseDir, provider, name)
	if err := os.WriteFile(path, []byte(body.SystemPrompt), 0644); err != nil {
		writeError(w, "Cannot write system prompt: "+err.Error(), "server_error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "system_prompt": body.SystemPrompt})
}

// **********************************************************************
// GET /api/health - Server-Status
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Circuit Breaker Details mit Enhanced Informationen
	type BreakerState struct {
		Model    string                 `json:"model"`
		Open     bool                   `json:"open"`
		Failures int                    `json:"failures"`
		Details  map[string]interface{} `json:"details,omitempty"`
	}

	var breakers []BreakerState
	for model, cb := range s.breakers {
		state := BreakerState{
			Model:    model,
			Open:     cb.IsOpen(),
			Failures: cb.Failures(),
			Details:  cb.GetStateDetails(),
		}
		breakers = append(breakers, state)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":           "ok",
		"timestamp":        time.Now().Unix(),
		"available_models": len(s.models),
		"circuit_breakers": breakers,
		"memory_set":       s.memory.Content != "",
	})
}

// **********************************************************************
// GET/PUT /api/memory - Memory-Block lesen und schreiben
func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		mem := s.memory
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mem)

	case http.MethodPut:
		var mem sigoengine.MemoryBlock
		if err := json.NewDecoder(r.Body).Decode(&mem); err != nil {
			writeError(w, "Invalid JSON: "+err.Error(), "invalid_request", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.memory = mem
		s.mu.Unlock()

		// Auf Disk persistieren
		data, _ := json.MarshalIndent(mem, "", "  ")
		if err := os.WriteFile(filepath.Join(s.baseDir, "memory.json"), data, 0644); err != nil {
			sigoengine.LogWarn("Memory auf Disk nicht gespeichert", map[string]interface{}{"error": err.Error()})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mem)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// **********************************************************************
// GET/PUT /api/system-prompt — Globalen System-Prompt lesen/setzen
func (s *Server) handleSystemPrompt(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		prompt := s.systemPrompt
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"system_prompt": prompt})

	case http.MethodPut:
		var body struct {
			SystemPrompt string `json:"system_prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, "Invalid JSON: "+err.Error(), "invalid_request", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.systemPrompt = body.SystemPrompt
		s.mu.Unlock()

		if err := os.WriteFile(filepath.Join(s.baseDir, "system-prompt.txt"), []byte(body.SystemPrompt), 0644); err != nil {
			sigoengine.LogWarn("system-prompt.txt speichern fehlgeschlagen", map[string]interface{}{"error": err.Error()})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":        "ok",
			"system_prompt": body.SystemPrompt,
		})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// **********************************************************************
// GET /api/help - API-Dokumentation
func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	help := map[string]interface{}{
		"name":        "sigoREST",
		"description": "OpenAI-kompatible REST-API für KI-Modelle",
		"version":     sigoengine.Version,
		"endpoints": []map[string]interface{}{
			{
				"path":        "/v1/chat/completions",
				"method":      "POST",
				"description": "OpenAI-kompatible Chat-Completion API",
				"parameters": map[string]string{
					"model":         "Modell-ID oder Shortcode (z.B. 'claude-h', 'gpt41')",
					"messages":      "Array von {role, content} Objekten",
					"temperature":   "Optional: 0.0-2.0 (default: Modell-Mittelwert)",
					"max_tokens":    "Optional: Max. Ausgabe-Tokens",
					"session_id":    "Optional: Session-ID für Gesprächsverlauf",
					"timeout":       "Optional: Timeout in Sekunden (default: 180)",
					"retries":       "Optional: Anzahl Retries (default: 3)",
					"channel":       "Optional: Kanal-FullName z.B. 'mammouth-0'",
					"stream":        "Optional: true für Server-Sent Events Streaming (OpenAI-kompatibel)",
					"bare":          "Optional: true → kein globaler/Kanal-Memory, kein Server-System-Prompt; nur ein nicht-leerer system_prompt zählt noch",
					"system_prompt": "Optional: per-Request System-Prompt-Override (bei bare:true der einzige Kontext)",
				},
				"example": `curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-h","messages":[{"role":"user","content":"Hallo"}]}'`,
			},
			{
				"path":        "/v1/messages",
				"method":      "POST",
				"description": "Anthropic-Messages-API-Bridge (Claude Code u.a.), Tool-Calling, alle Provider",
				"parameters": map[string]string{
					"model":       "Modell-ID oder Shortcode (z.B. 'claude-h', 'gpt41')",
					"max_tokens":  "Max. Ausgabe-Tokens (Anthropic-Pflichtfeld; 0 → Modell-Default)",
					"messages":    "Array von {role, content} Objekten (content: String oder Block-Array)",
					"system":      "Optional: System-Prompt (String oder Block-Array)",
					"tools":       "Optional: Array von Anthropic-Tool-Definitionen",
					"tool_choice": "Optional: Anthropic tool_choice-Objekt",
					"temperature": "Optional: Modell-Range (default: Modell-Mittelwert)",
					"stream":      "Optional: true für Anthropic-SSE-Event-Streaming",
				},
				"example": `curl -s http://localhost:9080/v1/messages \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-h","max_tokens":1024,"messages":[{"role":"user","content":"Hallo"}]}'`,
			},
			{
				"path":        "/v1/embeddings",
				"method":      "POST",
				"description": "OpenAI-kompatible Embedding-API (Proxy auf lokale Ollama-Modelle)",
				"parameters": map[string]string{
					"model": "Modell-ID oder Shortcode (z.B. 'ollama-nomic-embed-text-v2-moe')",
					"input": "String oder Array von Strings; leeres input → 400",
				},
				"example": `curl -s http://localhost:9080/v1/embeddings \
  -H "Content-Type: application/json" \
  -d '{"model":"ollama-nomic-embed-text-v2-moe","input":"Hallo Welt"}'`,
			},
			{
				"path":        "/v1/models",
				"method":      "GET",
				"description": "Liste aller verfügbaren Modelle (OpenAI-kompatibel)",
				"example":     "curl -s http://localhost:9080/v1/models",
			},
			{
				"path":        "/api/models",
				"method":      "GET",
				"description": "Detaillierte Modell-Informationen (Preise, Limits); ?format=csv liefert Registry-CSV (Semikolon, als models.csv nutzbar)",
				"example":     "curl -s http://localhost:9080/api/models  |  curl -s 'http://localhost:9080/api/models?format=csv' > models.csv",
			},
			{
				"path":        "/api/health",
				"method":      "GET",
				"description": "Server-Status, Circuit Breaker Zustand",
				"example":     "curl -s http://localhost:9080/api/health | jq",
			},
			{
				"path":        "/api/memory",
				"method":      "GET/PUT",
				"description": "Globaler Memory-Block lesen/schreiben",
				"parameters": map[string]string{
					"content": "System-Prompt für alle Anfragen",
					"cache":   "Boolean: Anthropic Caching aktivieren",
				},
				"example": `curl -s -X PUT http://localhost:9080/api/memory \
  -H "Content-Type: application/json" \
  -d '{"content":"Antworte immer auf Deutsch.","cache":true}'`,
			},
			{
				"path":        "/api/version",
				"method":      "GET",
				"description": "Versions-String von sigoREST",
				"example":     "curl -s http://localhost:9080/api/version",
			},
			{
				"path":        "/api/channels",
				"method":      "GET",
				"description": "Liste aller Kanäle mit Status",
				"example":     "curl -s http://localhost:9080/api/channels",
			},
			{
				"path":        "/api/channels/:provider/:name",
				"method":      "GET",
				"description": "Detail-Status eines Kanals",
				"example":     "curl -s http://localhost:9080/api/channels/mammouth/0",
			},
			{
				"path":        "/api/channels/:provider/:name/enable",
				"method":      "POST",
				"description": "Kanal aktivieren",
				"example":     "curl -s -X POST http://localhost:9080/api/channels/mammouth/0/enable",
			},
			{
				"path":        "/api/channels/:provider/:name/disable",
				"method":      "POST",
				"description": "Kanal deaktivieren",
				"example":     "curl -s -X POST http://localhost:9080/api/channels/mammouth/0/disable",
			},
			{
				"path":        "/api/channels/:provider/:name/memory",
				"method":      "GET/PUT",
				"description": "Kanal-spezifischen Memory lesen/schreiben",
				"example": `curl -s -X PUT http://localhost:9080/api/channels/mammouth/default/memory \
  -H "Content-Type: application/json" \
  -d '{"content":"Kanal-spezifischer Kontext","cache":false}'`,
			},
			{
				"path":        "/api/channels/:provider/:name/system-prompt",
				"method":      "GET/PUT",
				"description": "Kanal-spezifischen System-Prompt lesen/schreiben",
				"example": `curl -s -X PUT http://localhost:9080/api/channels/mammouth/default/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"Antworte immer auf Deutsch."}'`,
			},
			{
				"path":        "/api/usage",
				"method":      "GET",
				"description": "Token-Verbrauch pro Modell und pro Kanal",
				"example":     "curl -s http://localhost:9080/api/usage | jq",
			},
			{
				"path":        "/api/system-prompt",
				"method":      "GET/PUT",
				"description": "Globalen System-Prompt lesen/schreiben",
				"example": `curl -s -X PUT http://localhost:9080/api/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"Antworte immer auf Deutsch."}'`,
			},
			{
				"path":        "/api/help",
				"method":      "GET",
				"description": "Diese Hilfe-Dokumentation",
				"example":     "curl -s http://localhost:9080/api/help",
			},
		},
		"features": map[string]string{
			"circuit_breaker":        "Automatische Fehlerisolation nach 5 Fehlern in 60s",
			"retry":                  "Exponential Backoff: 500ms → 1s → 2s → max 5s",
			"session_management":     "JSON-basierte Sessions pro Kanal",
			"ip_access_control":      "HTTP: localhost, HTTPS: privates Netz",
			"ollama_discovery":       "Auto-Discovery lokaler Ollama-Modelle",
			"memory_block":           "Globaler + kanal-spezifischer Memory",
			"system_prompt":          "Globaler + kanal-spezifischer + per-Request System-Prompt",
			"multi_channel":          "Mehrere API-Key-Kanäle pro Provider mit Failover",
			"channel_health_monitor": "Automatische Health-Checks und Reserve-Zuschaltung",
			"bare_mode":              "bare:true in /v1/chat/completions unterdrückt Memory + Server-System-Prompt komplett",
			"usage_details":          "usage.prompt_tokens_details.cached_tokens, usage.completion_tokens_details.reasoning_tokens, usage.cost_usd (obere Schranke ohne Cache-Rabatt bei OpenAI-kompatiblen Providern)",
		},
		"error_types": map[string]string{
			"rate_limit":   "HTTP 429 - Zu viele Anfragen, Retry-After Header gesetzt",
			"auth_failed":  "HTTP 401 - Ungültiger API-Key",
			"timeout":      "HTTP 504 - Request Timeout",
			"server_error": "HTTP 503 - Upstream Server-Fehler",
			"client_error": "HTTP 400 - Ungültige Anfrage",
			"circuit_open": "HTTP 503 - Circuit Breaker geöffnet",
		},
		"environment_variables": map[string]string{
			"MAMMOUTH_API_KEY": "Für GPT, Claude, Gemini, Grok, DeepSeek, ...",
			"MOONSHOT_API_KEY": "Für Kimi Modelle (direkt)",
			"ZAI_API_KEY":      "Für GLM Modelle (direkt)",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(help)
}

// **********************************************************************
// GET /api/usage - kumulierte Token-Statistiken
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.usageMu.RLock()
	snapshot := make(map[string]*ModelUsageStats, len(s.usage))
	for k, v := range s.usage {
		cp := *v
		snapshot[k] = &cp
	}
	snapshotByChannel := make(map[string]*ModelUsageStats, len(s.usageByChannel))
	for k, v := range s.usageByChannel {
		cp := *v
		snapshotByChannel[k] = &cp
	}
	s.usageMu.RUnlock()

	// Gesamt-Summe über alle Kanäle berechnen
	var total ModelUsageStats
	for _, v := range snapshotByChannel {
		total.InputTokens += v.InputTokens
		total.OutputTokens += v.OutputTokens
		total.TotalTokens += v.TotalTokens
		total.Requests += v.Requests
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"by_model":   snapshot,
		"by_channel": snapshotByChannel,
		"total":      total,
	})
}

// **********************************************************************
// Hilfsfunktion für Fehler-Antworten
func writeError(w http.ResponseWriter, msg, errType string, status int) {
	var resp ErrorResponse
	resp.Error.Message = msg
	resp.Error.Type = errType
	resp.Error.Code = errType
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// jsonEscapeString escaped a string for JSON embedding
func jsonEscapeString(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// newChannelRegistry baut die Kanal-Registry: Provider mit API-Key aus ENV,
// Ollama als keyloser Kanal (nur wenn beim Start Modelle gefunden wurden),
// danach der persistierte Status aus channels.json. Reihenfolge wichtig:
// erst registrieren, dann LoadState — sonst würde ein per API deaktivierter
// Kanal beim Neustart wieder aktiv.
func newChannelRegistry(baseDir string, ollamaAvailable bool) *sigoengine.ChannelRegistry {
	registry := sigoengine.NewChannelRegistry(filepath.Join(baseDir, "channels.json"))
	registry.DiscoverFromEnv()
	if ollamaAvailable {
		registry.AddKeylessChannel("ollama")
	}
	if err := registry.LoadState(); err != nil {
		sigoengine.LogWarn("Kanal-Status konnte nicht geladen werden", map[string]interface{}{"error": err.Error()})
	}
	return registry
}

// **********************************************************************
// main
func main() {
	flag.Parse()

	// .env im Startverzeichnis laden (optional, Fallback auf echte Env).
	// Veraltete ./env wird noch geladen, die Warnung erst nach der
	// Log-Konfiguration ausgegeben (damit -q/-v greifen).
	envWarning, err := sigoengine.LoadDefaultEnvFile(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fehler beim Laden der env-Datei: %v\n", err)
		os.Exit(1)
	}

	if *showVersion {
		fmt.Printf("sigoREST Version %s\n", sigoengine.Version)
		os.Exit(0)
	}

	sigoengine.SetLogLevel(sigoengine.ParseLogLevel(*logLevel))
	sigoengine.SetJSONMode(*jsonLogs)
	sigoengine.SetQuietMode(*quiet)
	if envWarning != "" {
		sigoengine.LogWarn(envWarning)
	}

	// Kommunikationsprotokoll (optional). Explizit angefordert → Fehler beim
	// Öffnen ist fatal statt still ohne Protokoll weiterzulaufen.
	if *commLogPath != "" {
		commLog, err := sigoengine.OpenCommLog(*commLogPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Fehler beim Öffnen des Kommunikationsprotokolls: %v\n", err)
			os.Exit(1)
		}
		defer commLog.Close()
		sigoengine.SetCommLog(commLog)
		sigoengine.LogWarn("Kommunikationsprotokoll aktiv — enthält vollständige Prompts/Antworten", map[string]interface{}{
			"path": *commLogPath,
		})
	}

	sigoengine.LogInfo("sigoREST startet", map[string]interface{}{
		"http_port":  *httpPort,
		"https_port": *httpsPort,
	})

	// TLS-Zertifikat sicherstellen
	if err := ensureTLSCert(*certFile, *keyFile); err != nil {
		sigoengine.LogError("TLS-Zertifikat Fehler", err, nil)
		os.Exit(1)
	}

	// Persistente ID-Registry (id_registry.db im data-dir) VOR dem
	// Modell-Laden öffnen, damit loadModelsFromProviders die einmalig
	// vergebenen Shortcodes übernehmen kann. Ein Fehlschlag ist nicht
	// fatal — Shortcodes werden dann wie vor diesem Feature pro Boot neu
	// berechnet (nil-safe analog zu costDB).
	idRegistry, err := sigoengine.OpenIDRegistry(*dataDir)
	if err != nil {
		sigoengine.LogWarn("ID-Registry konnte nicht geöffnet werden — Shortcodes werden pro Boot neu berechnet", map[string]interface{}{"error": err.Error()})
		idRegistry = nil
	} else {
		defer idRegistry.Close()
		sigoengine.LogInfo("ID-Registry aktiv", map[string]interface{}{"path": idRegistry.Path()})
	}

	// Server-State initialisieren
	srv := &Server{
		models:         loadModelsFromProviders(idRegistry),
		memory:         loadMemory(*dataDir),
		breakers:       make(map[string]*sigoengine.EnhancedCircuitBreaker),
		systemPrompt:   loadSystemPrompt(*dataDir),
		usage:          make(map[string]*ModelUsageStats),
		usageByChannel: make(map[string]*ModelUsageStats),
		baseDir:        *dataDir,
		idRegistry:     idRegistry,
	}

	// Datenverzeichnis anlegen falls nicht vorhanden
	if _, err := os.Stat(srv.baseDir); os.IsNotExist(err) {
		if err := os.MkdirAll(srv.baseDir, 0755); err != nil {
			sigoengine.LogWarn("Konnte Datenverzeichnis nicht anlegen", map[string]interface{}{"error": err.Error(), "path": srv.baseDir})
		}
	}

	// Persistentes Kosten-Tracking (costs.db im data-dir). Ein Fehlschlag ist
	// nicht fatal — der Server läuft dann ohne Kosten-Persistenz weiter
	// (recordUsage prüft s.costDB == nil und überspringt den DB-Write).
	costDB, err := sigoengine.OpenCostDB(srv.baseDir)
	if err != nil {
		sigoengine.LogWarn("Kosten-DB konnte nicht geöffnet werden — Tracking deaktiviert", map[string]interface{}{"error": err.Error()})
	} else {
		srv.costDB = costDB
		defer costDB.Close()
		sigoengine.LogInfo("Kosten-DB aktiv", map[string]interface{}{"path": costDB.Path()})
	}

	// Ollama Auto-Discovery — vor der Kanal-Registry, weil davon abhängt,
	// ob ein (keyloser) Ollama-Kanal registriert wird.
	ollamaAvailable := sigoengine.DiscoverOllamaModels(ollamaEndpoint) > 0
	if ollamaAvailable {
		srv.mu.Lock()
		ollamaModels := sigoengine.GetOllamaModels()
		for sc, info := range ollamaModels {
			srv.models[sc] = ModelInfo{
				ID:              sc,
				Shortcode:       sc,
				Endpoint:        "http://localhost:11434/v1/chat/completions",
				APIKey:          "",
				MaxInputTokens:  info.ContextLength,
				MaxOutputTokens: 0,
				MinTemperature:  0.0,
				MaxTemperature:  2.0,
			}
		}
		srv.mu.Unlock()
	}

	// Kanal-Registry initialisieren
	srv.channelManager = sigoengine.NewChannelManager(newChannelRegistry(srv.baseDir, ollamaAvailable))
	srv.rateLimiter = sigoengine.NewRateLimiter()
	srv.rateMinInterval = *rateMinInterval
	srv.rateMaxWait = *rateMaxWait
	sigoengine.LogInfo("Rate-Limiter aktiv", map[string]interface{}{
		"min_interval_ms": srv.rateMinInterval.Milliseconds(),
		"max_wait_ms":     srv.rateMaxWait.Milliseconds(),
	})

	// Health-Monitor starten. Health-Status aktiver Kanäle wird lazy aus
	// echten User-Requests gesetzt (handleChatCompletions → MarkChannelHealth).
	// Der Ticker aktiviert nur noch Reserven per kostenlosem /models-Probe.
	sigoengine.StartHealthMonitor(context.Background(), srv.channelManager, *channelHealthInterval)

	sigoengine.LogInfo("Konfiguration geladen", map[string]interface{}{
		"available_models": len(srv.models),
		"memory_cache":     srv.memory.Cache,
	})

	// HTTP-Mux einmal erstellen (beide Listener nutzen dieselben Handler)
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", srv.handlePing)
	mux.HandleFunc("/api/version", srv.handleVersion)
	mux.HandleFunc("/v1/chat/completions", srv.handleChatCompletions)
	mux.HandleFunc("/v1/embeddings", srv.handleEmbeddings)
	mux.HandleFunc("/v1/messages", srv.handleMessages)
	mux.HandleFunc("/v1/models", srv.handleModels)
	mux.HandleFunc("/api/models", srv.handleAPIModels)
	mux.HandleFunc("/api/shortcodes", srv.handleShortcodes)
	mux.HandleFunc("/api/shortlist", srv.handleShortlist)
	mux.HandleFunc("/api/channels/", srv.handleChannelRouter)
	mux.HandleFunc("/api/channels", srv.handleChannels)
	mux.HandleFunc("/api/health", srv.handleHealth)
	mux.HandleFunc("/api/memory", srv.handleMemory)
	mux.HandleFunc("/api/system-prompt", srv.handleSystemPrompt)
	mux.HandleFunc("/api/usage", srv.handleUsage)
	mux.HandleFunc("/api/costs", srv.handleCosts)
	mux.HandleFunc("/api/budget", srv.handleBudget)
	mux.HandleFunc("/api/help", srv.handleHelp)

	// HTTP-Server (nur localhost)
	httpHandler := serverHeaderMiddleware(ipMiddleware(isLocalhost, mux))
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", *httpPort),
		Handler:      httpHandler,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: streamWriteDeadlineExtension, // AI-Calls können lang dauern; Streams erneuern das pro Chunk
		IdleTimeout:  300 * time.Second,
	}

	// HTTPS-Server (privates Netz)
	httpsHandler := serverHeaderMiddleware(ipMiddleware(isPrivateNet, mux))

	tlsCert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		sigoengine.LogError("TLS-Zertifikat laden fehlgeschlagen", err, nil)
		os.Exit(1)
	}

	httpsServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", *httpsPort),
		Handler: httpsHandler,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			MinVersion:   tls.VersionTLS12,
		},
		ReadTimeout:  90 * time.Second,
		WriteTimeout: streamWriteDeadlineExtension,
		IdleTimeout:  300 * time.Second,
	}

	// Beide Server parallel starten
	errCh := make(chan error, 2)

	go func() {
		sigoengine.LogInfo("HTTP-Server startet", map[string]interface{}{
			"addr": httpServer.Addr, "allowed": "127.0.0.0/8",
		})
		if err := httpServer.ListenAndServe(); err != nil {
			errCh <- fmt.Errorf("HTTP: %w", err)
		}
	}()

	go func() {
		sigoengine.LogInfo("HTTPS-Server startet", map[string]interface{}{
			"addr": httpsServer.Addr, "allowed": "192.168.0.0/16, 10.0.0.0/8",
		})
		if err := httpsServer.ListenAndServeTLS("", ""); err != nil {
			errCh <- fmt.Errorf("HTTPS: %w", err)
		}
	}()

	// Auf Fehler warten
	err = <-errCh
	sigoengine.LogError("Server-Fehler", err, nil)
	os.Exit(1)
}
