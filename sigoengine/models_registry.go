//**********************************************************************
//      sigoengine/models_registry.go
//**********************************************************************
//  Beschreibung: Registry-Logik mit Lookup-Maps und Override-Support
//                Lädt Modelle aus: JSON → CSV → CoreModels (Fallback)
//**********************************************************************

package sigoengine

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var (
	modelsByID         map[string]Model
	modelsByShortcode  map[string]Model
	registryOnce       sync.Once
	registryMu         sync.RWMutex
	overrideModelsPath string // Benutzerdefinierter Pfad für models.csv
)

// initRegistry initialisiert die Lookup-Maps (einmalig)
func initRegistry() {
	registryOnce.Do(func() {
		modelsByID = make(map[string]Model)
		modelsByShortcode = make(map[string]Model)

		// Ladereihenfolge: JSON → CSV → CoreModels
		models := loadModelsWithOverride()

		for _, m := range models {
			modelsByID[m.ID] = m
			modelsByShortcode[m.Shortcode] = m
		}
	})
}

// SetModelsCSVPath setzt einen benutzerdefinierten Pfad für models.csv
// Muss vor dem ersten Registry-Zugriff aufgerufen werden
func SetModelsCSVPath(path string) {
	overrideModelsPath = path
}

// GetModelsCSVPath gibt den gesetzten Custom-Pfad zurück (oder leer wenn nicht gesetzt)
func GetModelsCSVPath() string {
	return overrideModelsPath
}

// loadModelsWithOverride lädt Modelle mit Override-Priorität
// Priorität: Custom Path (Flag) → ~/.config/sigorest/models.json → ~/.config/sigorest/models.csv
//           → sigoREST/models.csv (Projekt) → CoreModels (Fallback)
func loadModelsWithOverride() []Model {
	// 1. Custom Pfad (wenn gesetzt via -models Flag)
	if overrideModelsPath != "" {
		if models, err := loadModelsFromCSV(overrideModelsPath); err == nil {
			return models
		}
	}

	configDir, err := os.UserConfigDir()
	if err == nil {
		sigorestDir := filepath.Join(configDir, "sigorest")

		// Versuche JSON zuerst (User-Override)
		jsonPath := filepath.Join(sigorestDir, "models.json")
		if models, err := loadModelsFromJSON(jsonPath); err == nil {
			return models
		}

		// Dann CSV (User-Override)
		csvPath := filepath.Join(sigorestDir, "models.csv")
		if models, err := loadModelsFromCSV(csvPath); err == nil {
			return models
		}
	}

	// Versuche system-weite models.csv (im sigoREST Verzeichnis)
	// Prüfe mehrere mögliche Pfade
	possiblePaths := []string{
		"sigoREST/models.csv",                          // Relativ zum Working Dir
		"../sigoREST/models.csv",                       // Eben höher
		"/usr/local/slib/sigoREST/models.csv",          // System-Installation
		"models.csv",                                   // Im aktuellen Verzeichnis
	}

	for _, path := range possiblePaths {
		if models, err := loadModelsFromCSV(path); err == nil {
			return models
		}
	}

	// Fallback zu eingebetteten CoreModels
	return CoreModels
}

// loadModelsFromJSON lädt Modelle aus einer JSON-Datei
func loadModelsFromJSON(path string) ([]Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var models []Model
	if err := json.Unmarshal(data, &models); err != nil {
		return nil, fmt.Errorf("invalid JSON format: %w", err)
	}

	// Validiere Modelle
	for i, m := range models {
		if m.ID == "" || m.Shortcode == "" || m.Endpoint == "" {
			return nil, fmt.Errorf("model at index %d missing required fields (id/shortcode/endpoint)", i)
		}
	}

	return models, nil
}

// loadModelsFromCSV lädt Modelle aus einer CSV-Datei (Semikolon-getrennt)
func loadModelsFromCSV(path string) ([]Model, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = ';'
	reader.Comment = '#'
	reader.FieldsPerRecord = -1 // Variable Felder erlauben

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV parse error: %w", err)
	}

	var models []Model
	for i, record := range records {
		if len(record) < 3 {
			continue // Überspringe leere/unvollständige Zeilen
		}

		model, err := parseCSVRecord(record)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i+1, err)
		}

		if model.ID != "" { // Überspringe Kommentarzeilen
			models = append(models, model)
		}
	}

	return models, nil
}

// parseCSVRecord parst einen CSV-Record zu einem Model
// Format: id;shortcode;endpoint;apikey;max_input;max_output;input_cost;output_cost;min_temp;max_temp;requires_completion_tokens
func parseCSVRecord(record []string) (Model, error) {
	// Trimme Whitespace von allen Feldern
	for i := range record {
		record[i] = strings.TrimSpace(record[i])
	}

	m := Model{
		ID:        record[0],
		Shortcode: record[1],
		Endpoint:  record[2],
	}

	// Optionale Felder mit Defaults
	if len(record) > 3 {
		m.APIKeyEnv = record[3]
	}

	if len(record) > 4 && record[4] != "" {
		if v, err := strconv.Atoi(record[4]); err == nil {
			m.MaxInputTokens = v
		}
	}

	if len(record) > 5 && record[5] != "" {
		if v, err := strconv.Atoi(record[5]); err == nil {
			m.MaxOutputTokens = v
		}
	}

	if len(record) > 6 && record[6] != "" {
		if v, err := strconv.ParseFloat(record[6], 64); err == nil {
			m.InputCost = v
		}
	}

	if len(record) > 7 && record[7] != "" {
		if v, err := strconv.ParseFloat(record[7], 64); err == nil {
			m.OutputCost = v
		}
	}

	if len(record) > 8 && record[8] != "" {
		if v, err := strconv.ParseFloat(record[8], 64); err == nil {
			m.MinTemperature = v
		}
	}

	if len(record) > 9 && record[9] != "" {
		if v, err := strconv.ParseFloat(record[9], 64); err == nil {
			m.MaxTemperature = v
		}
	}

	if len(record) > 10 && record[10] != "" {
		m.RequiresCompletionTokens = strings.ToLower(record[10]) == "true"
	}

	return m, nil
}

// GetModelByID sucht ein Modell anhand seiner ID
func GetModelByID(id string) (Model, bool) {
	initRegistry()
	registryMu.RLock()
	defer registryMu.RUnlock()
	m, ok := modelsByID[id]
	return m, ok
}

// GetModelByShortcode sucht ein Modell anhand seines Shortcodes
func GetModelByShortcode(shortcode string) (Model, bool) {
	initRegistry()
	registryMu.RLock()
	defer registryMu.RUnlock()
	m, ok := modelsByShortcode[shortcode]
	return m, ok
}

// GetAllModels gibt alle verfügbaren Modelle zurück
func GetAllModels() []Model {
	initRegistry()
	registryMu.RLock()
	defer registryMu.RUnlock()

	result := make([]Model, 0, len(modelsByID))
	seen := make(map[string]bool)
	for _, m := range modelsByID {
		if !seen[m.ID] {
			result = append(result, m)
			seen[m.ID] = true
		}
	}
	return result
}

// ResolveModelName resolved einen Namen zu einer vollständigen Modell-ID
// Unterstützt: ID direkt, Shortcode, oder ID ohne Präfixe
func ResolveModelName(name string) string {
	initRegistry()
	registryMu.RLock()
	defer registryMu.RUnlock()

	// Direkte ID-Match
	if _, ok := modelsByID[name]; ok {
		return name
	}

	// Shortcode-Match
	if m, ok := modelsByShortcode[name]; ok {
		return m.ID
	}

	// Fallback: Gib den Namen unverändert zurück (für Ollama-Modelle etc.)
	return name
}

// AddOllamaModel fügt ein zur Laufzeit entdecktes Ollama-Modell hinzu
func AddOllamaModel(name, endpoint string) {
	initRegistry()
	registryMu.Lock()
	defer registryMu.Unlock()

	shortname := name
	if idx := strings.Index(shortname, ":latest"); idx != -1 {
		shortname = shortname[:idx]
	}
	sc := GenerateShortcode("ollama-"+shortname, nil)
	// Fallback falls GenerateShortcode keinen ollama-Prefix erkennt
	if !strings.HasPrefix(sc, "ollama") {
		sc = "ollama-" + shortname
		sc = strings.ReplaceAll(sc, ":", "-")
	}

	m := Model{
		ID:              name,
		Shortcode:       sc,
		Endpoint:        endpoint,
		APIKeyEnv:       "",
		MaxInputTokens:  0, // Unbekannt bei Ollama
		MaxOutputTokens: 0,
		MinTemperature:  0.0,
		MaxTemperature:  2.0,
	}

	modelsByID[name] = m
	modelsByShortcode[sc] = m
}

// generateOllamaShortcode ist deprecated – wird von GenerateShortcode ersetzt

// GetModelDefaultTokens gibt die Standard-Token-Anzahl für ein Modell zurück
func GetModelDefaultTokens(modelName string) int {
	id := ResolveModelName(modelName)
	if m, ok := GetModelByID(id); ok && m.MaxOutputTokens > 0 {
		return m.MaxOutputTokens
	}
	return 4096 // Sicherer Default
}

// GetModelTemperatureRange gibt Min, Max und Default-Temperatur zurück
func GetModelTemperatureRange(modelName string) (min, max, def float64) {
	min, max, def = 0.0, 2.0, 1.0 // Defaults

	id := ResolveModelName(modelName)
	if m, ok := GetModelByID(id); ok {
		if m.MinTemperature != 0 || m.MaxTemperature != 0 {
			min, max = m.MinTemperature, m.MaxTemperature
		}
	}
	return min, max, def
}

// ModelExists prüft ob ein Modell (ID oder Shortcode) existiert
func ModelExists(name string) bool {
	initRegistry()
	registryMu.RLock()
	defer registryMu.RUnlock()

	if _, ok := modelsByID[name]; ok {
		return true
	}
	if _, ok := modelsByShortcode[name]; ok {
		return true
	}
	return false
}
