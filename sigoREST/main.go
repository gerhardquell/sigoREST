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
	"math/big"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "embed"

	"sigorest/sigoengine"
)

// **********************************************************************
// Embedded Default-Dateien
//
//go:embed models.csv
var defaultModelsCSV string

//go:embed memory.json
var defaultMemoryJSON string

// **********************************************************************
// MemoryBlock - globaler Kontext-Block für alle Anfragen
type MemoryBlock struct {
	Content string `json:"content"`
	Cache   bool   `json:"cache"`
}

// **********************************************************************
// ModelInfo - Modell-Informationen aus CSV
type ModelInfo struct {
	ID                       string  `json:"id"`
	Shortcode                string  `json:"shortcode"`
	Endpoint                 string  `json:"endpoint"`
	APIKey                   string  `json:"apikey"`
	MaxInputTokens           int     `json:"max_input_tokens"`
	MaxOutputTokens          int     `json:"max_output_tokens"`
	InputCost                float64 `json:"input_cost"`   // $/1M tokens
	OutputCost               float64 `json:"output_cost"`  // $/1M tokens
	MinTemperature           float64 `json:"min_temperature"`
	MaxTemperature          float64 `json:"max_temperature"`
	RequiresCompletionTokens bool    `json:"requires_completion_tokens"`
}

// **********************************************************************
// Server-State
type Server struct {
	mu       sync.RWMutex
	memory   MemoryBlock
	models   map[string]ModelInfo                        // id → ModelInfo
	breakers map[string]*sigoengine.CircuitBreaker         // Modell → Circuit Breaker
}

// **********************************************************************
// Server-Konfiguration (Flags)
var (
	httpPort  = flag.Int("http-port", 9080, "HTTP-Port für localhost")
	httpsPort = flag.Int("https-port", 9443, "HTTPS-Port für privates Netz")
	certFile  = flag.String("cert", "./certs/server.crt", "TLS-Zertifikat")
	keyFile   = flag.String("key", "./certs/server.key", "TLS-Schlüssel")
	logLevel  = flag.String("v", "info", "Log-Level: debug|info|warn|error")
	quiet     = flag.Bool("q", false, "Quiet Mode")
	jsonLogs  = flag.Bool("j", false, "JSON-Logs")
)

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

// isLocalhost prüft ob die IP im 127.0.0.0/8 Bereich liegt
func isLocalhost(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return localhostCIDR.Contains(ip)
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
// Modelle aus CSV laden

// loadModels liest models.csv (Disk hat Vorrang vor embedded)
func loadModels() map[string]ModelInfo {
	var csvContent string

	data, err := os.ReadFile("./models.csv")
	if err == nil {
		csvContent = string(data)
		sigoengine.LogInfo("models.csv von Disk geladen")
	} else {
		csvContent = defaultModelsCSV
		sigoengine.LogInfo("models.csv (embedded default) verwendet")
	}

	models := make(map[string]ModelInfo)
	lines := strings.Split(strings.TrimSpace(csvContent), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, ";")
		if len(parts) < 10 {
			sigoengine.LogWarn("Ungültige CSV-Zeile ignoriert (weniger als 10 Felder)", map[string]interface{}{"line": line})
			continue
		}

		id := strings.TrimSpace(parts[0])
		shortcode := strings.TrimSpace(parts[1])
		endpoint := strings.TrimSpace(parts[2])
		apikey := strings.TrimSpace(parts[3])
		maxInput, _ := strconv.Atoi(parts[4])
		maxOutput, _ := strconv.Atoi(parts[5])
		inputCost, _ := strconv.ParseFloat(parts[6], 64)
		outputCost, _ := strconv.ParseFloat(parts[7], 64)
		minTemp, _ := strconv.ParseFloat(parts[8], 64)
		maxTemp, _ := strconv.ParseFloat(parts[9], 64)
		requiresCompletion := len(parts) > 10 && strings.TrimSpace(parts[10]) == "true"

		models[id] = ModelInfo{
			ID:                       id,
			Shortcode:                shortcode,
			Endpoint:                 endpoint,
			APIKey:                   apikey,
			MaxInputTokens:           maxInput,
			MaxOutputTokens:          maxOutput,
			InputCost:                inputCost,
			OutputCost:               outputCost,
			MinTemperature:           minTemp,
			MaxTemperature:           maxTemp,
			RequiresCompletionTokens: requiresCompletion,
		}
	}

	sigoengine.LogInfo("Modelle geladen", map[string]interface{}{"count": len(models)})
	return models
}

// **********************************************************************
// Memory-Block laden

// loadMemory liest memory.json (Disk hat Vorrang vor embedded)
func loadMemory() MemoryBlock {
	var jsonContent []byte

	data, err := os.ReadFile("./memory.json")
	if err == nil {
		jsonContent = data
		sigoengine.LogInfo("memory.json von Disk geladen")
	} else {
		jsonContent = []byte(defaultMemoryJSON)
		sigoengine.LogInfo("memory.json (embedded default) verwendet")
	}

	var mem MemoryBlock
	if err := json.Unmarshal(jsonContent, &mem); err != nil {
		sigoengine.LogWarn("memory.json Parse-Fehler, verwende leer", map[string]interface{}{"error": err.Error()})
	}
	return mem
}

// **********************************************************************
// Request/Response Typen (OpenAI-kompatibel)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	Temp      float64       `json:"temperature"`
	MaxTokens int           `json:"max_tokens"`
	SessionID string        `json:"session_id"` // sigoREST-Erweiterung
	Timeout   int           `json:"timeout"`    // sigoREST-Erweiterung
	Retries   int           `json:"retries"`    // sigoREST-Erweiterung
}

type ChatChoice struct {
	Index   int         `json:"index"`
	Message ChatMessage `json:"message"`
}

type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
}

type ErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
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

	// Modell-Validierung (ID oder Shortcode)
	modelID := req.Model

	// Prüfe ob es ein Shortcode ist und resolven
	s.mu.RLock()
	modelInfo, exists := s.models[modelID]
	if !exists {
		// Versuche Shortcode zu resolven
		for _, info := range s.models {
			if info.Shortcode == modelID {
				modelID = info.ID
				modelInfo = info
				exists = true
				break
			}
		}
	}
	if !exists {
		s.mu.RUnlock()
		writeError(w, fmt.Sprintf("Model '%s' nicht gefunden", req.Model), "model_not_found", http.StatusBadRequest)
		return
	}
	mem := s.memory
	s.mu.RUnlock()

	// Config aus ModelInfo aufbauen
	cfg := &sigoengine.ProviderConfig{
		Endpoint: modelInfo.Endpoint,
		Model:    modelID,
		APIKey:   os.Getenv(modelInfo.APIKey),
	}

	// Defaults setzen
	if req.MaxTokens == 0 && modelInfo.MaxOutputTokens > 0 {
		req.MaxTokens = modelInfo.MaxOutputTokens
	}
	if req.Temp == 0 {
		if modelInfo.MinTemperature < modelInfo.MaxTemperature {
			req.Temp = (modelInfo.MinTemperature + modelInfo.MaxTemperature) / 2.0
		} else {
			req.Temp = 1.0
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

	// Memory-Block als System-Message (immer zuerst)
	if mem.Content != "" {
		memMsg := map[string]interface{}{
			"role":    "system",
			"content": mem.Content,
		}
		// Anthropic prompt caching (nur für Anthropic-Modelle via anthropic-Typ)
		// Für Mammoth/OpenAI: automatisches Caching bei >=1024 Tokens
		messages = append(messages, memMsg)
	}

	// Session-History laden und einbauen
	if req.SessionID != "" {
		session := sigoengine.LoadSession(req.SessionID, req.Model)
		for _, m := range session.History {
			messages = append(messages, map[string]interface{}{
				"role": m.Role, "content": m.Content,
			})
		}
	}

	// User-Messages aus Request (außer system, die kommt von Memory)
	var userPrompt string
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			// User-definierter system-prompt wird NACH memory eingefügt
			messages = append(messages, map[string]interface{}{
				"role": "system", "content": msg.Content,
			})
		} else {
			messages = append(messages, map[string]interface{}{
				"role": msg.Role, "content": msg.Content,
			})
			if msg.Role == "user" {
				userPrompt = msg.Content
			}
		}
	}

	// API-Request aufbauen
	apiRequest := map[string]interface{}{
		"model":       cfg.Model,
		"messages":    messages,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temp,
	}
	// GPT-5: max_completion_tokens statt max_tokens
	if modelInfo.RequiresCompletionTokens {
		delete(apiRequest, "max_tokens")
		apiRequest["max_completion_tokens"] = req.MaxTokens
	}

	// Circuit Breaker pro Modell
	s.mu.Lock()
	if _, exists := s.breakers[req.Model]; !exists {
		s.breakers[req.Model] = sigoengine.NewCircuitBreaker()
	}
	breaker := s.breakers[req.Model]
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.Timeout)*time.Second)
	defer cancel()

	var responseText string
	var lastErr error

	for i := 0; i < req.Retries; i++ {
		err := breaker.Do(func() error {
			text, e := sigoengine.CallAPI(ctx, cfg, apiRequest, req.Timeout)
			if e != nil {
				return e
			}
			responseText = text
			return nil
		})
		if err == nil {
			break
		}
		lastErr = err
		sigoengine.LogWarn("API-Fehler, Retry", map[string]interface{}{
			"attempt": i + 1, "retries": req.Retries, "model": req.Model,
		})
		if i < req.Retries-1 {
			time.Sleep(time.Duration(i+1) * time.Second)
		}
	}

	if lastErr != nil {
		sigoengine.LogError("API-Call fehlgeschlagen", lastErr, map[string]interface{}{"model": req.Model})
		writeError(w, lastErr.Error(), "api_error", http.StatusBadGateway)
		return
	}

	// Session speichern
	if req.SessionID != "" && userPrompt != "" {
		session := sigoengine.LoadSession(req.SessionID, req.Model)
		session.AddMessage("user", userPrompt)
		session.AddMessage("assistant", responseText)
		session.Save(req.SessionID, req.Model)
	}

	// OpenAI-kompatible Antwort
	resp := ChatResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []ChatChoice{{
			Index:   0,
			Message: ChatMessage{Role: "assistant", Content: responseText},
		}},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
		// ID und Shortcode hinzufügen
		models = append(models, ModelData{
			ID:      id,
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "sigorest",
		})
		if info.Shortcode != id {
			models = append(models, ModelData{
				ID:      info.Shortcode,
				Object:  "model",
				Created: time.Now().Unix(),
				OwnedBy: "sigorest",
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
// GET /api/models - Volle Modell-Infos
func (s *Server) handleAPIModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Ollama-Modelle zur Liste hinzufügen
	ollamaModels := sigoengine.GetOllamaModels()

	var models []ModelInfo
	for id, info := range s.models {
		// Cloud-Modell
		models = append(models, ModelInfo{
			ID:                       id,
			Shortcode:                info.Shortcode,
			Endpoint:                 info.Endpoint,
			APIKey:                   info.APIKey,
			MaxInputTokens:           info.MaxInputTokens,
			MaxOutputTokens:          info.MaxOutputTokens,
			InputCost:                info.InputCost,
			OutputCost:               info.OutputCost,
			MinTemperature:           info.MinTemperature,
			MaxTemperature:           info.MaxTemperature,
			RequiresCompletionTokens: info.RequiresCompletionTokens,
		})
	}

	// Ollama-Modelle hinzufügen
	for sc := range ollamaModels {
		models = append(models, ModelInfo{
			ID:                       sc,
			Shortcode:                sc,
			Endpoint:                 "http://localhost:11434/v1/chat/completions",
			APIKey:                   "",
			MaxInputTokens:           0,
			MaxOutputTokens:          0,
			InputCost:                0,
			OutputCost:               0,
			MinTemperature:           0.0,
			MaxTemperature:           2.0,
			RequiresCompletionTokens: false,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models)
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

	type BreakerState struct {
		Model    string `json:"model"`
		Open     bool   `json:"open"`
		Failures int    `json:"failures"`
	}

	var breakers []BreakerState
	for model, cb := range s.breakers {
		breakers = append(breakers, BreakerState{
			Model:    model,
			Open:     cb.IsOpen(),
			Failures: cb.Failures(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"timestamp":      time.Now().Unix(),
		"available_models": len(s.models),
		"circuit_breakers": breakers,
		"memory_set":     s.memory.Content != "",
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
		var mem MemoryBlock
		if err := json.NewDecoder(r.Body).Decode(&mem); err != nil {
			writeError(w, "Invalid JSON: "+err.Error(), "invalid_request", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.memory = mem
		s.mu.Unlock()

		// Auf Disk persistieren
		data, _ := json.MarshalIndent(mem, "", "  ")
		if err := os.WriteFile("./memory.json", data, 0644); err != nil {
			sigoengine.LogWarn("Memory auf Disk nicht gespeichert", map[string]interface{}{"error": err.Error()})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mem)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
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

// **********************************************************************
// main
func main() {
	flag.Parse()

	sigoengine.SetLogLevel(sigoengine.ParseLogLevel(*logLevel))
	sigoengine.SetJSONMode(*jsonLogs)
	sigoengine.SetQuietMode(*quiet)

	sigoengine.LogInfo("sigoREST startet", map[string]interface{}{
		"http_port":  *httpPort,
		"https_port": *httpsPort,
	})

	// TLS-Zertifikat sicherstellen
	if err := ensureTLSCert(*certFile, *keyFile); err != nil {
		sigoengine.LogError("TLS-Zertifikat Fehler", err, nil)
		os.Exit(1)
	}

	// Server-State initialisieren
	srv := &Server{
		models:   loadModels(),
		memory:   loadMemory(),
		breakers: make(map[string]*sigoengine.CircuitBreaker),
	}

	// Ollama Auto-Discovery
	ollamaEndpoint := "http://localhost:11434"
	if n := sigoengine.DiscoverOllamaModels(ollamaEndpoint); n > 0 {
		srv.mu.Lock()
		ollamaModels := sigoengine.GetOllamaModels()
		for sc := range ollamaModels {
			// Ollama-Modell zur models Map hinzufügen
			srv.models[sc] = ModelInfo{
				ID:                       sc,
				Shortcode:                sc,
				Endpoint:                 "http://localhost:11434/v1/chat/completions",
				APIKey:                   "",
				MaxInputTokens:           0,
				MaxOutputTokens:          0,
				InputCost:                0,
				OutputCost:               0,
				MinTemperature:           0.0,
				MaxTemperature:           2.0,
				RequiresCompletionTokens: false,
			}
		}
		srv.mu.Unlock()
	}

	sigoengine.LogInfo("Konfiguration geladen", map[string]interface{}{
		"available_models": len(srv.models),
		"memory_cache":     srv.memory.Cache,
	})

	// HTTP-Mux einmal erstellen (beide Listener nutzen dieselben Handler)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", srv.handleChatCompletions)
	mux.HandleFunc("/v1/models", srv.handleModels)
	mux.HandleFunc("/api/models", srv.handleAPIModels)
	mux.HandleFunc("/api/health", srv.handleHealth)
	mux.HandleFunc("/api/memory", srv.handleMemory)

	// HTTP-Server (nur localhost)
	httpHandler := ipMiddleware(isLocalhost, mux)
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", *httpPort),
		Handler:      httpHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute, // AI-Calls können lang dauern
		IdleTimeout:  120 * time.Second,
	}

	// HTTPS-Server (privates Netz)
	httpsHandler := ipMiddleware(isPrivateNet, mux)

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
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
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
