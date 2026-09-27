//**********************************************************************
//      sigoengine/env.go
//**********************************************************************
//  Beschreibung: Unterstützung für .env-Datei im Startverzeichnis
//                (veraltete ./env wird mit Warnung noch geladen)
//**********************************************************************

package sigoengine

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// DotEnvFileName ist der bevorzugte Dateiname (versteckt, nicht im ls sichtbar).
	DotEnvFileName = ".env"
	// LegacyEnvFileName ist der veraltete, sichtbare Dateiname.
	LegacyEnvFileName = "env"
)

// ResolveEnvFile wählt die im Verzeichnis dir zu ladende Env-Datei.
// Rückgabe:
//   - path:    zu ladender Pfad, "" wenn keine Env-Datei existiert
//   - warning: Hinweis für den Aufrufer (z.B. veraltete ./env), "" wenn keiner
//
// Reine Funktion (nur os.Stat), kein globaler Zustand — dadurch testbar.
func ResolveEnvFile(dir string) (path string, warning string) {
	dotEnv := filepath.Join(dir, DotEnvFileName)
	legacy := filepath.Join(dir, LegacyEnvFileName)
	_, errDot := os.Stat(dotEnv)
	_, errLegacy := os.Stat(legacy)
	hasDot := errDot == nil
	hasLegacy := errLegacy == nil

	// Absoluter Pfad nur für die Warnung: unter systemd sieht man sonst
	// nicht, in welchem Arbeitsverzeichnis die Datei liegt.
	legacyAbs := legacy
	if abs, err := filepath.Abs(legacy); err == nil {
		legacyAbs = abs
	}

	switch {
	case hasDot && hasLegacy:
		return dotEnv, fmt.Sprintf("veraltete Env-Datei %q wird ignoriert, %s hat Vorrang — bitte löschen",
			legacyAbs, DotEnvFileName)
	case hasDot:
		return dotEnv, ""
	case hasLegacy:
		return legacy, fmt.Sprintf("veraltete Env-Datei %q geladen — bitte in %s umbenennen",
			legacyAbs, DotEnvFileName)
	default:
		return "", ""
	}
}

// LoadDefaultEnvFile lädt die Env-Datei aus dir (siehe ResolveEnvFile).
// Die Warnung wird NICHT selbst geloggt, weil die Aufrufer die Env-Datei
// vor SetLogLevel/SetQuietMode laden — sie sollen sie danach per LogWarn
// ausgeben, damit -q/-v respektiert werden.
func LoadDefaultEnvFile(dir string) (warning string, err error) {
	path, warning := ResolveEnvFile(dir)
	if path == "" {
		return warning, nil
	}
	return warning, LoadEnvFile(path)
}

var (
	envFileVars  = make(map[string]string)
	envFileOnce  sync.Once
	envFilePath  string
	envFileError error
)

// LoadEnvFile lädt Variablen aus einer env-Datei in eine interne Map.
// Existiert die Datei nicht, wird das stillschweigend ignoriert.
// Jede Zeile im Format KEY=VALUE. Leere Zeilen und # Kommentare werden übersprungen.
func LoadEnvFile(path string) error {
	envFileOnce.Do(func() {
		envFilePath = path
		file, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				envFileError = nil
				return
			}
			envFileError = fmt.Errorf("cannot read env file %q: %w", path, err)
			return
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// Entferne optionale Anführungszeichen
			if len(value) >= 2 {
				if (value[0] == '"' && value[len(value)-1] == '"') ||
					(value[0] == '\'' && value[len(value)-1] == '\'') {
					value = value[1 : len(value)-1]
				}
			}
			envFileVars[key] = value
		}
		envFileError = scanner.Err()
	})
	return envFileError
}

// GetEnvWithFile gibt den Wert einer Variable zurück.
// Reihenfolge: 1) env-Datei (falls geladen), 2) echte Environment-Variable.
func GetEnvWithFile(envVar string) string {
	if v, ok := envFileVars[envVar]; ok {
		return v
	}
	return os.Getenv(envVar)
}

// EnvFileLoaded reports whether LoadEnvFile has been called.
func EnvFileLoaded() bool {
	return envFilePath != ""
}
