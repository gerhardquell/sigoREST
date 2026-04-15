//**********************************************************************
//      cmd/sigoE/main.go
//**********************************************************************
//  Beschreibung: CLI-Wrapper für sigoengine Package
//                Identisches Verhalten zum originalen sigoEngine Binary
//                Behebt Bug: Default-Modell war "kimi-turbo" (existiert nicht)
//**********************************************************************

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sigorest/sigoengine"
)

const version = "1.0"

func main() {
	var (
		model        = flag.String("m", "gpt41", "Modell (Shortcode oder vollständiger Name)")
		sessionID    = flag.String("s", "", "Session-ID für Gesprächsverlauf")
		maxTokens    = flag.Int("n", 0, "Max. Tokens (0 = Modell-Default)")
		timeout      = flag.Int("t", sigoengine.DEFAULT_TIMEOUT, "Timeout in Sekunden")
		retries      = flag.Int("r", 3, "Anzahl Wiederholungsversuche")
		quiet        = flag.Bool("q", false, "Quiet Mode (nur Fehler)")
		jsonOut      = flag.Bool("j", false, "JSON-Ausgabe")
		help         = flag.Bool("h", false, "Hilfe anzeigen")
		listModels   = flag.Bool("l", false, "Alle verfügbaren Modelle anzeigen")
		temperature  = flag.Float64("T", -1.0, "Temperatur (-1 = Modell-Default)")
		systemPrompt = flag.String("sp", "", "System-Prompt")
		showInfo     = flag.Bool("i", false, "Modell-Info anzeigen")
		logLevel     = flag.String("v", "info", "Log-Level: debug|info|warn|error")
		showVersion  = flag.Bool("V", false, "Version anzeigen")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("sigoE Version %s\n", version)
		os.Exit(0)
	}

	sigoengine.SetLogLevel(sigoengine.ParseLogLevel(*logLevel))
	sigoengine.SetJSONMode(*jsonOut)
	sigoengine.SetQuietMode(*quiet)

	modelName := sigoengine.ResolveModelName(*model)

	if *maxTokens == 0 {
		*maxTokens = sigoengine.GetModelDefaultTokens(modelName)
	}
	if *temperature == -1.0 {
		_, _, *temperature = sigoengine.GetModelTemperatureRange(modelName)
	}

	if *help {
		showHelp()
		return
	}
	if *listModels {
		listAllModels()
		return
	}
	if *showInfo {
		showModelInfo(modelName)
		return
	}

	cfg, err := sigoengine.LoadConfig(*model)
	if err != nil {
		sigoengine.LogError("Konfiguration nicht geladen", err, nil)
		os.Exit(1)
	}

	prompt, err := getInput()
	if err != nil || prompt == "" {
		sigoengine.LogError("Kein Prompt", err, nil)
		os.Exit(2)
	}

	session := sigoengine.LoadSession(*sessionID, *model)

	// Request aufbauen
	messages := []map[string]string{}
	if *systemPrompt != "" {
		messages = append(messages, map[string]string{"role": "system", "content": *systemPrompt})
	}
	for _, m := range session.BuildMessages(prompt) {
		messages = append(messages, m)
	}

	request := map[string]interface{}{
		"model":       cfg.Model,
		"messages":    messages,
		"max_tokens":  *maxTokens,
		"temperature": *temperature,
	}

	// Enhanced Circuit Breaker mit konfigurierbaren Parametern
	cbConfig := sigoengine.DefaultCircuitBreakerConfig()
	breaker := sigoengine.NewEnhancedCircuitBreaker(cbConfig)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()

	var responseText string
	start := time.Now()

	// Exponential Backoff Retry
	retryConfig := sigoengine.DefaultRetryConfig()
	retryConfig.MaxRetries = *retries

	err = sigoengine.RetryWithBackoff(ctx, retryConfig, func() error {
		return breaker.Do(func() error {
			text, e := sigoengine.CallAPI(ctx, cfg, request, *timeout)
			if e != nil {
				return e
			}
			responseText = text
			return nil
		})
	})

	duration := time.Since(start) / time.Millisecond

	if err != nil {
		// Fehler klassifizieren für bessere Fehlermeldungen
		apiErr := sigoengine.ClassifyError(err)

		if *jsonOut {
			resp := sigoengine.Response{
				Model: *model, PID: os.Getpid(),
				Timestamp: time.Now().Unix(),
				Error:     apiErr.Error(),
				Duration:  duration,
			}
			json.NewEncoder(os.Stdout).Encode(resp)
		} else if !*quiet {
			// Menschenlesbare Fehlermeldung mit Typ
			fmt.Fprintf(os.Stderr, "\nFehler [%s]: %s\n", apiErr.Type, apiErr.Message)
			if apiErr.StatusCode > 0 {
				fmt.Fprintf(os.Stderr, "HTTP Status: %d\n", apiErr.StatusCode)
			}
			if apiErr.Type == sigoengine.ErrCircuitOpen {
				fmt.Fprintln(os.Stderr, "Der Circuit Breaker ist geöffnet. Bitte warte kurz und versuche es erneut.")
			}
			if apiErr.Type == sigoengine.ErrRateLimit && apiErr.RetryAfter > 0 {
				fmt.Fprintf(os.Stderr, "Retry-After: %.0f Sekunden\n", apiErr.RetryAfter.Seconds())
			}
		}
		os.Exit(1)
	}

	// Session speichern
	if *sessionID != "" {
		session.AddMessage("user", prompt)
		session.AddMessage("assistant", responseText)
		session.Save(*sessionID, *model)
	}

	if *jsonOut {
		resp := sigoengine.Response{
			Model: *model, PID: os.Getpid(),
			Timestamp: time.Now().Unix(),
			Prompt:    prompt, Response: responseText,
			Duration: duration,
		}
		json.NewEncoder(os.Stdout).Encode(resp)
	} else {
		fmt.Println(responseText)
	}
}

func getInput() (string, error) {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		return strings.TrimSpace(string(data)), err
	}
	// Interaktiver Modus
	reader := bufio.NewReader(os.Stdin)
	fmt.Fprint(os.Stderr, "Prompt: ")
	input, err := reader.ReadString('\n')
	return strings.TrimSpace(input), err
}

func showHelp() {
	fmt.Println("sigoEngine - SI Gateway in GO")
	fmt.Println("Usage: sigoE [Optionen] [Prompt]")
	fmt.Println("       echo 'Prompt' | sigoE [Optionen]")
	fmt.Println("       sigoE [Optionen] < datei.txt")
	fmt.Println()
	flag.PrintDefaults()
	fmt.Println("\nSessions:")
	sessions, _ := filepath.Glob(".sessions/*.json")
	for _, s := range sessions {
		base := filepath.Base(s)
		parts := strings.Split(strings.TrimSuffix(base, ".json"), "-")
		if len(parts) >= 2 {
			fmt.Printf("  -s %s (model: %s)\n", strings.Join(parts[1:], "-"), parts[0])
		}
	}
}

func listAllModels() {
	fmt.Println("Verfügbare Modelle:")
	fmt.Println("===================")
	fmt.Println()

	type modelEntry struct {
		name, shortcode string
		inCost, outCost float64
		provider        string
	}

	var entries []modelEntry
	for _, m := range sigoengine.GetAllModels() {
		var provider string
		switch {
		case strings.Contains(m.Endpoint, "mammouth"):
			provider = "Mammoth.ai"
		case strings.Contains(m.Endpoint, "moonshot"):
			provider = "Moonshot"
		case strings.Contains(m.Endpoint, "z.ai"):
			provider = "Z.ai"
		default:
			provider = "Other"
		}
		entries = append(entries, modelEntry{
			name:      m.ID,
			shortcode: m.Shortcode,
			inCost:    m.InputCost,
			outCost:   m.OutputCost,
			provider:  provider,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].provider != entries[j].provider {
			return entries[i].provider < entries[j].provider
		}
		return entries[i].shortcode < entries[j].shortcode
	})

	curProvider := ""
	for _, e := range entries {
		if e.provider != curProvider {
			if curProvider != "" {
				fmt.Println()
			}
			fmt.Printf("--- %s ---\n", e.provider)
			fmt.Printf("%-20s %-12s %10s %10s\n", "Modell", "Shortcode", "Input$/M", "Output$/M")
			fmt.Println(strings.Repeat("-", 58))
			curProvider = e.provider
		}
		fmt.Printf("%-20s %-12s %10.2f %10.2f\n", e.name, e.shortcode, e.inCost, e.outCost)
	}
}

func showModelInfo(modelName string) {
	m, exists := sigoengine.GetModelByID(modelName)
	if !exists {
		// Versuche über Shortcode zu finden
		m, exists = sigoengine.GetModelByShortcode(modelName)
		if !exists {
			fmt.Printf("Modell '%s' nicht gefunden\n", modelName)
			return
		}
	}
	fmt.Printf("\nModell: %s\n", m.ID)
	fmt.Printf("Shortcode:   %s\n", m.Shortcode)
	fmt.Printf("Endpoint:    %s\n", m.Endpoint)
	fmt.Printf("API Key Env: %s\n", m.APIKeyEnv)
	fmt.Printf("Max Context: %d Tokens\n", m.MaxInputTokens)
	fmt.Printf("Max Output:  %d Tokens\n", m.MaxOutputTokens)
	fmt.Printf("Temperatur:  %.1f - %.1f (Default: %.1f)\n", m.MinTemperature, m.MaxTemperature, (m.MinTemperature+m.MaxTemperature)/2.0)
	fmt.Printf("Preis Input: $%.2f/M Tokens\n", m.InputCost)
	fmt.Printf("Preis Output:$%.2f/M Tokens\n", m.OutputCost)
}
