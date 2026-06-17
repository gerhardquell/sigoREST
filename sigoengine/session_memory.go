//**********************************************************************
//      sigoengine/session_memory.go
//**********************************************************************
//  Beschreibung: Hilfsfunktionen für Session- und Memory-Pfade
//**********************************************************************

package sigoengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultSessionBaseDir is the CLI default for session storage.
const DefaultSessionBaseDir = ".sessions"

// DefaultServerBaseDir is the server default for all persisted state.
const DefaultServerBaseDir = "/var/sigoREST"

// SessionPath returns the full path for a session file.
func SessionPath(baseDir, provider, channel, model, sessionID string) string {
	return filepath.Join(baseDir, "sessions", provider, channel, fmt.Sprintf("%s-%s.json", model, sessionID))
}

// ChannelMemoryPath returns the path for a channel memory file.
func ChannelMemoryPath(baseDir, provider, channel string) string {
	return filepath.Join(baseDir, "channels", provider, channel, "memory.json")
}

// ChannelSystemPromptPath returns the path for a channel system prompt file.
func ChannelSystemPromptPath(baseDir, provider, channel string) string {
	return filepath.Join(baseDir, "channels", provider, channel, "system-prompt.txt")
}

// EnsureSessionDir creates the session directory for a provider/channel.
func EnsureSessionDir(baseDir, provider, channel string) error {
	return os.MkdirAll(filepath.Join(baseDir, "sessions", provider, channel), 0755)
}

// EnsureChannelDir creates the channel directory for memory/system-prompt.
func EnsureChannelDir(baseDir, provider, channel string) error {
	return os.MkdirAll(filepath.Join(baseDir, "channels", provider, channel), 0755)
}

// LoadSessionForChannel lädt eine Session aus dem kanal-spezifischen Pfad.
func LoadSessionForChannel(baseDir, provider, channel, sessionID, model string) *Session {
	if sessionID == "" {
		return &Session{}
	}
	path := SessionPath(baseDir, provider, channel, model, sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		return &Session{}
	}
	var s Session
	json.Unmarshal(data, &s)
	return &s
}

// SaveForChannel speichert eine Session im kanal-spezifischen Pfad.
func (s *Session) SaveForChannel(baseDir, provider, channel, sessionID, model string) {
	if sessionID == "" {
		return
	}
	EnsureSessionDir(baseDir, provider, channel)
	path := SessionPath(baseDir, provider, channel, model, sessionID)
	data, _ := json.Marshal(s)
	os.WriteFile(path, data, 0644)
}
