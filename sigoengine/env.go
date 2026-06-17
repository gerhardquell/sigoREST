//**********************************************************************
//      sigoengine/env.go
//**********************************************************************
//  Beschreibung: Unterstützung für env-Datei im Startverzeichnis
//**********************************************************************

package sigoengine

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

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
