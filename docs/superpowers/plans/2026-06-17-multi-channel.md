# Multi-Channel Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement parallel AI provider channels with per-channel API keys, memory, sessions, health monitoring, failover, and a centralized version string exposed via CLI and REST.

**Architecture:** New `sigoengine/channel*.go` files model channels and their registry/manager. The REST server uses the manager to resolve channels for each chat request, fail over on retryable errors, and runs a background health monitor. Per-channel memory/sessions are persisted under `/var/sigoREST`. The version constant moves to `sigoengine/version.go` so both binaries share it.

**Tech Stack:** Go 1.26, module `sigorest`, `go test`, `encoding/json`, `sync`, `os`, standard library only.

## Global Constraints

- Module name: `sigorest`
- Go version: 1.26
- All new exported identifiers use English names and Go conventions.
- All files must start with the existing project header comment block (author/copyright/description).
- Thread-safety required wherever the server holds shared mutable state.
- Logging uses `sigoengine.LogDebug/LogInfo/LogWarn/LogError`.
- Errors use `sigoengine.NewError` with `Err*` codes.
- Tests live in `sigoengine/*_test.go` and run with `go test ./sigoengine/ -v`.
- Server/CLI have no Go tests — manual testing via API/CLI.
- Frequent commits; each task ends with a commit.
- YAGNI: do not implement Ollama channels, channel quotas, or UI.

---

## File Structure Overview

| File | Responsibility |
|---|---|
| `sigoengine/version.go` | Central version constant `Version` |
| `sigoengine/channel.go` | `Channel` struct, `ChannelRegistry`, env discovery |
| `sigoengine/channel_manager.go` | `ChannelManager`, channel resolution, failover |
| `sigoengine/channel_health.go` | Health monitor goroutine |
| `sigoengine/session_memory.go` | Per-channel session/memory path helpers |
| `sigoengine/loadconfig_channel.go` | Extend `LoadConfig` to accept a channel |
| `sigoREST/main.go` | Wire channels into chat handler, add `/api/version`, `/api/channels*`, health monitor startup |
| `cmd/sigoE/main.go` | Add `-c`, `-session-dir`, `-version` flags |
| `sigoengine/channel_test.go` | Tests for registry and discovery |
| `sigoengine/channel_manager_test.go` | Tests for resolution and failover |
| `docs/systemd-install.md` | Add `/var/sigoREST` setup instructions |

---

### Task 1: Central Version Constant

**Files:**
- Create: `sigoengine/version.go`
- Modify: `sigoREST/main.go` (remove local `version`, use `sigoengine.Version`)
- Modify: `cmd/sigoE/main.go` (add `-version` flag)

**Interfaces:**
- Produces: `const sigoengine.Version = "1.1"`

- [ ] **Step 1: Create `sigoengine/version.go`**

```go
//**********************************************************************
//      sigoengine/version.go
//**********************************************************************
//  Beschreibung: Zentrale Versions-Information für sigoREST und sigoE
//**********************************************************************

package sigoengine

// Version ist die aktuelle Version von sigoREST/sigoE.
// Wird von CLI und REST gemeinsam verwendet.
const Version = "1.1"
```

- [ ] **Step 2: Modify `sigoREST/main.go` to use central version**

Find:
```go
const version = "1.0"
```

Replace with nothing (delete the line).

Find all uses of `version` in `sigoREST/main.go` and replace with `sigoengine.Version`.

Expected replacements:
- `fmt.Sprintf("sigoREST/%s", version)` → `fmt.Sprintf("sigoREST/%s", sigoengine.Version)`
- `fmt.Printf("sigoREST Version %s\n", version)` → `fmt.Printf("sigoREST Version %s\n", sigoengine.Version)`
- `"version":     version,` → `"version":     sigoengine.Version,`

- [ ] **Step 3: Add `-version` flag to CLI**

In `cmd/sigoE/main.go`, add a new flag near the other flags:

```go
showVersion := flag.Bool("version", false, "Version anzeigen")
```

After `flag.Parse()`, add:

```go
if *showVersion {
    fmt.Printf("sigoE %s\n", sigoengine.Version)
    os.Exit(0)
}
```

- [ ] **Step 4: Build both binaries**

Run:
```bash
go build ./...
```

Expected: success, no errors.

- [ ] **Step 5: Verify versions**

Run:
```bash
go build -o sigoREST/sigoREST ./sigoREST/
go build -o sigoE ./cmd/sigoE/
./sigoREST/sigoREST -version
./sigoE -version
```

Expected output for both:
```
sigoREST 1.1
sigoE 1.1
```

- [ ] **Step 6: Commit**

```bash
git add sigoengine/version.go sigoREST/main.go cmd/sigoE/main.go
git commit -m "feat: zentrale Versions-Konstante für Server und CLI

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: Channel Data Model

**Files:**
- Create: `sigoengine/channel.go`

**Interfaces:**
- Produces: `type Channel struct{...}`, `type ChannelRegistry struct{...}`

- [ ] **Step 1: Create `sigoengine/channel.go` with base types**

```go
//**********************************************************************
//      sigoengine/channel.go
//**********************************************************************
//  Beschreibung: Kanal-Datenmodell und Registry für Multi-Channel-Support
//**********************************************************************

package sigoengine

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Channel beschreibt einen API-Key-Kanal eines Providers.
type Channel struct {
	Provider          string    `json:"provider"`
	Name              string    `json:"name"`
	APIKey            string    `json:"-"` // nie serialisieren
	Active            bool      `json:"active"`
	Order             int       `json:"order"`
	Healthy           bool      `json:"healthy"`
	LastHealthCheck   time.Time `json:"last_health_check,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	ConsecutiveErrors int       `json:"consecutive_errors"`
}

// FullName returns the canonical channel identifier, e.g. "mammouth-0".
func (c *Channel) FullName() string {
	return fmt.Sprintf("%s-%s", c.Provider, c.Name)
}

// ChannelRegistry hält alle bekannten Kanäle pro Provider.
type ChannelRegistry struct {
	mu        sync.RWMutex
	channels  map[string][]*Channel // provider → sorted channels
	statePath string                // path to channels.json
}

// NewChannelRegistry creates an empty registry.
func NewChannelRegistry(statePath string) *ChannelRegistry {
	return &ChannelRegistry{
		channels:  make(map[string][]*Channel),
		statePath: statePath,
	}
}

// Channels returns a copy of all channels for a provider, sorted by Order.
func (r *ChannelRegistry) Channels(provider string) []*Channel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := r.channels[provider]
	result := make([]*Channel, len(list))
	copy(result, list)
	return result
}

// AllProviders returns the list of known providers.
func (r *ChannelRegistry) AllProviders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := make([]string, 0, len(r.channels))
	for p := range r.channels {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	return providers
}

// GetChannel returns a channel by provider and name.
func (r *ChannelRegistry) GetChannel(provider, name string) (*Channel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, ch := range r.channels[provider] {
		if ch.Name == name {
			return ch, true
		}
	}
	return nil, false
}

// GetChannelByFullName returns a channel by its full name, e.g. "mammouth-0".
func (r *ChannelRegistry) GetChannelByFullName(fullName string) (*Channel, bool) {
	parts := strings.SplitN(fullName, "-", 2)
	if len(parts) != 2 {
		return nil, false
	}
	return r.GetChannel(parts[0], parts[1])
}

// SetActive changes the active flag of a channel and persists state.
func (r *ChannelRegistry) SetActive(provider, name string, active bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ch := range r.channels[provider] {
		if ch.Name == name {
			ch.Active = active
			if !active {
				ch.Healthy = false
			}
			return r.saveStateLocked()
		}
	}
	return NewError(ErrConfigNotFound, "channel not found", nil,
		map[string]interface{}{"provider": provider, "channel": name})
}

// AddChannel adds or replaces a channel in the registry.
func (r *ChannelRegistry) AddChannel(ch *Channel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.channels[ch.Provider]
	found := false
	for i, existing := range list {
		if existing.Name == ch.Name {
			list[i] = ch
			found = true
			break
		}
	}
	if !found {
		list = append(list, ch)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Order < list[j].Order
	})
	r.channels[ch.Provider] = list
}
```

- [ ] **Step 2: Build**

Run:
```bash
go build ./sigoengine/
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add sigoengine/channel.go
git commit -m "feat: Channel- und ChannelRegistry-Datenmodell

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: Discover Channels from Environment

**Files:**
- Modify: `sigoengine/channel.go`

**Interfaces:**
- Consumes: `Channel`, `ChannelRegistry.AddChannel`
- Produces: `func (r *ChannelRegistry) DiscoverFromEnv()`, helper `channelOrderFromName`

- [ ] **Step 1: Add env discovery to `sigoengine/channel.go`**

Append to `channel.go`:

```go
// knownProviders maps the base API-key env var names to provider names and
// the default endpoint used by that provider.
// This list matches the providers in CoreModels and the server fetchers.
var knownProviders = []struct {
	EnvVar   string
	Provider string
}{
	{"MAMMOUTH_API_KEY", "mammouth"},
	{"MOONSHOT_API_KEY", "moonshot"},
	{"ZAI_API_KEY", "zai"},
}

// DiscoverFromEnv scans environment variables for provider API keys.
// It always registers the default channel (unindexed key) and any indexed
// channels (PROVIDER_API_KEY_0, _1, ...) that are set.
func (r *ChannelRegistry) DiscoverFromEnv() {
	for _, p := range knownProviders {
		// Default channel
		if key := os.Getenv(p.EnvVar); key != "" {
			r.AddChannel(&Channel{
				Provider: p.Provider,
				Name:     "default",
				APIKey:   key,
				Active:   true,
				Order:    0,
				Healthy:  true,
			})
		}

		// Indexed channels
		for i := 0; ; i++ {
			envName := fmt.Sprintf("%s_%d", p.EnvVar, i)
			key := os.Getenv(envName)
			if key == "" {
				break
			}
			name := fmt.Sprintf("%d", i)
			r.AddChannel(&Channel{
				Provider: p.Provider,
				Name:     name,
				APIKey:   key,
				Active:   false,
				Order:    i + 1,
				Healthy:  false,
			})
		}
	}
}
```

- [ ] **Step 2: Build**

Run:
```bash
go build ./sigoengine/
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add sigoengine/channel.go
git commit -m "feat: API-Key Kanäle aus Env-Variablen erkennen

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: Channel State Persistence

**Files:**
- Modify: `sigoengine/channel.go`

**Interfaces:**
- Consumes: `ChannelRegistry.channels`, `Channel.Active`
- Produces: `LoadState()`, `saveStateLocked()`

- [ ] **Step 1: Add persistence methods to `channel.go`**

Append to `channel.go`:

```go
// persistedState is the on-disk shape of channels.json.
type persistedState struct {
	Providers map[string]map[string]struct {
		Active bool `json:"active"`
	} `json:"providers"`
}

// LoadState reads channels.json and applies saved active flags.
// It never creates API keys; channels must already be discovered from env.
func (r *ChannelRegistry) LoadState() error {
	if r.statePath == "" {
		return nil
	}
	data, err := os.ReadFile(r.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return NewError(ErrSessionError, "cannot read channel state", err,
			map[string]interface{}{"path": r.statePath})
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return NewError(ErrSessionError, "invalid channel state file", err,
			map[string]interface{}{"path": r.statePath})
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for provider, channels := range state.Providers {
		for name, cfg := range channels {
			for _, ch := range r.channels[provider] {
				if ch.Name == name {
					ch.Active = cfg.Active
					if !ch.Active {
						ch.Healthy = false
					}
				}
			}
		}
	}
	return nil
}

// SaveState persists the current active flags to disk.
func (r *ChannelRegistry) SaveState() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saveStateLocked()
}

func (r *ChannelRegistry) saveStateLocked() error {
	if r.statePath == "" {
		return nil
	}
	state := persistedState{Providers: make(map[string]map[string]struct {
		Active bool `json:"active"`
	})}
	for provider, list := range r.channels {
		m := make(map[string]struct {
			Active bool `json:"active"`
		})
		for _, ch := range list {
			m[ch.Name] = struct {
				Active bool `json:"active"`
			}{Active: ch.Active}
		}
		state.Providers[provider] = m
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return NewError(ErrSessionError, "cannot marshal channel state", err, nil)
	}
	if err := os.MkdirAll(filepath.Dir(r.statePath), 0755); err != nil {
		return NewError(ErrSessionError, "cannot create channel state dir", err,
			map[string]interface{}{"path": r.statePath})
	}
	if err := os.WriteFile(r.statePath, data, 0644); err != nil {
		return NewError(ErrSessionError, "cannot write channel state", err,
			map[string]interface{}{"path": r.statePath})
	}
	return nil
}
```

- [ ] **Step 2: Add missing import `path/filepath`**

Update the imports in `channel.go` to include:

```go
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)
```

- [ ] **Step 3: Build**

Run:
```bash
go build ./sigoengine/
```

Expected: success.

- [ ] **Step 4: Commit**

```bash
git add sigoengine/channel.go
git commit -m "feat: channels.json Persistenz für Kanal-Status

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: Channel Manager (Resolution + Failover)

**Files:**
- Create: `sigoengine/channel_manager.go`

**Interfaces:**
- Consumes: `ChannelRegistry`, `Channel`
- Produces: `ChannelManager`, `Resolve(provider, requested string) (*Channel, error)`, `NextActive(provider string, after *Channel) (*Channel, bool)`

- [ ] **Step 1: Create `sigoengine/channel_manager.go`**

```go
//**********************************************************************
//      sigoengine/channel_manager.go
//**********************************************************************
//  Beschreibung: Kanal-Auflösung und Failover-Logik
//**********************************************************************

package sigoengine

import (
	"fmt"
)

// ErrChannelInactive signals that a specifically requested channel is not active.
const ErrChannelInactive = "CHANNEL_INACTIVE"

// ChannelManager wraps a registry and provides resolution/failover helpers.
type ChannelManager struct {
	registry *ChannelRegistry
}

// NewChannelManager creates a manager for the given registry.
func NewChannelManager(registry *ChannelRegistry) *ChannelManager {
	return &ChannelManager{registry: registry}
}

// Registry returns the underlying registry.
func (m *ChannelManager) Registry() *ChannelRegistry {
	return m.registry
}

// Resolve picks a channel for a provider.
// If requested is empty, returns the first active channel in order.
// If requested is set, returns that channel only if it exists and is active.
func (m *ChannelManager) Resolve(provider, requested string) (*Channel, error) {
	if requested != "" {
		ch, ok := m.registry.GetChannel(provider, requested)
		if !ok {
			return nil, NewError(ErrConfigNotFound, "channel not found", nil,
				map[string]interface{}{"provider": provider, "channel": requested})
		}
		if !ch.Active {
			return nil, NewError(ErrChannelInactive, "requested channel is inactive", nil,
				map[string]interface{}{"provider": provider, "channel": requested})
		}
		return ch, nil
	}

	for _, ch := range m.registry.Channels(provider) {
		if ch.Active {
			return ch, nil
		}
	}
	return nil, NewError(ErrConfigNotFound, "no active channel for provider", nil,
		map[string]interface{}{"provider": provider})
}

// NextActive returns the next active channel after the given one, in order.
// If after is nil, returns the first active channel.
// The bool return indicates whether a channel was found.
func (m *ChannelManager) NextActive(provider string, after *Channel) (*Channel, bool) {
	channels := m.registry.Channels(provider)
	passed := after == nil
	for _, ch := range channels {
		if !passed {
			if ch.Name == after.Name {
				passed = true
			}
			continue
		}
		if ch.Active {
			return ch, true
		}
	}
	return nil, false
}

// AllChannelStatus returns a snapshot of every known channel.
func (m *ChannelManager) AllChannelStatus() []map[string]interface{} {
	var result []map[string]interface{}
	for _, provider := range m.registry.AllProviders() {
		for _, ch := range m.registry.Channels(provider) {
			result = append(result, map[string]interface{}{
				"provider":            ch.Provider,
				"name":                ch.Name,
				"full_name":           ch.FullName(),
				"active":              ch.Active,
				"healthy":             ch.Healthy,
				"last_health_check":   ch.LastHealthCheck,
				"last_error":          ch.LastError,
				"consecutive_errors":  ch.ConsecutiveErrors,
			})
		}
	}
	return result
}
```

- [ ] **Step 2: Build**

Run:
```bash
go build ./sigoengine/
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add sigoengine/channel_manager.go
git commit -m "feat: ChannelManager mit Auflösung und Failover

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: LoadConfig with Channel Support

**Files:**
- Modify: `sigoengine/engine.go` (`LoadConfig` signature)
- Create: `sigoengine/loadconfig_channel.go`

**Interfaces:**
- Consumes: `Channel`
- Produces: `LoadConfigWithChannel(model string, ch *Channel) (*ProviderConfig, error)`, update `LoadConfig` to call it with default channel

- [ ] **Step 1: Refactor `LoadConfig` to accept channel**

In `sigoengine/engine.go`, replace the `LoadConfig` function with:

```go
// LoadConfig lädt die Konfiguration für ein Modell aus der Registry + ENV
// unter Verwendung des Default-Kanals.
func LoadConfig(model string) (*ProviderConfig, error) {
	return LoadConfigWithChannel(model, nil)
}
```

- [ ] **Step 2: Create `sigoengine/loadconfig_channel.go`**

```go
//**********************************************************************
//      sigoengine/loadconfig_channel.go
//**********************************************************************
//  Beschreibung: LoadConfig-Erweiterung für Kanal-Auswahl
//**********************************************************************

package sigoengine

import (
	"os"
)

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

	apiKey := os.Getenv(m.APIKeyEnv)
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
```

- [ ] **Step 3: Build**

Run:
```bash
go build ./sigoengine/
```

Expected: success.

- [ ] **Step 4: Commit**

```bash
git add sigoengine/engine.go sigoengine/loadconfig_channel.go
git commit -m "feat: LoadConfig unterstützt Kanal-Auswahl

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: Session and Memory Path Helpers

**Files:**
- Create: `sigoengine/session_memory.go`
- Modify: `sigoengine/engine.go` (`LoadSession`, `Save`)

**Interfaces:**
- Consumes: `Channel`
- Produces: `SessionDir(baseDir, provider, channel string)`, `ChannelMemoryPath(baseDir, provider, channel string)`, `ChannelSystemPromptPath(...)`, updated `LoadSession/Save` to use a configurable base directory

- [ ] **Step 1: Create `sigoengine/session_memory.go`**

```go
//**********************************************************************
//      sigoengine/session_memory.go
//**********************************************************************
//  Beschreibung: Hilfsfunktionen für Session- und Memory-Pfade
//**********************************************************************

package sigoengine

import (
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
```

- [ ] **Step 2: Update `LoadSession` and `Save` to accept baseDir**

In `sigoengine/engine.go`, change:

```go
// LoadSession lädt eine Session aus einer JSON-Datei
func LoadSession(sessionID, model string) *Session {
	if sessionID == "" {
		return &Session{}
	}
	path := fmt.Sprintf(".sessions/%s-%s.json", model, sessionID)
	...
}
```

to:

```go
// LoadSession lädt eine Session aus einer JSON-Datei
func LoadSession(sessionID, model string) *Session {
	return LoadSessionFromDir(DefaultSessionBaseDir, sessionID, model)
}

// LoadSessionFromDir lädt eine Session aus dem angegebenen Basisverzeichnis.
func LoadSessionFromDir(baseDir, sessionID, model string) *Session {
	if sessionID == "" {
		return &Session{}
	}
	path := SessionPath(baseDir, "", "", model, sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		return &Session{}
	}
	var s Session
	json.Unmarshal(data, &s)
	return &s
}
```

And change:

```go
// Save speichert eine Session auf Disk
func (s *Session) Save(sessionID, model string) {
	if sessionID == "" {
		return
	}
	os.MkdirAll(".sessions", 0755)
	path := fmt.Sprintf(".sessions/%s-%s.json", model, sessionID)
	...
}
```

to:

```go
// Save speichert eine Session auf Disk
func (s *Session) Save(sessionID, model string) {
	s.SaveToDir(DefaultSessionBaseDir, sessionID, model)
}

// SaveToDir speichert eine Session unter dem angegebenen Basisverzeichnis.
func (s *Session) SaveToDir(baseDir, sessionID, model string) {
	if sessionID == "" {
		return
	}
	os.MkdirAll(filepath.Join(baseDir, "sessions"), 0755)
	path := SessionPath(baseDir, "", "", model, sessionID)
	data, _ := json.Marshal(s)
	os.WriteFile(path, data, 0644)
}
```

Wait: `SessionPath` currently builds `baseDir/sessions/provider/channel/...`. For backward compatibility the legacy `LoadSession/Save` must keep using `.sessions/<model>-<session>.json`. The new per-channel functions will use `LoadSessionFromDir(baseDir, provider, channel, sessionID, model)`.

Therefore add overloads:

```go
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
```

Keep the original `LoadSession` and `Save` unchanged for backward compatibility.

- [ ] **Step 3: Build**

Run:
```bash
go build ./sigoengine/
```

Expected: success.

- [ ] **Step 4: Commit**

```bash
git add sigoengine/session_memory.go sigoengine/engine.go
git commit -m "feat: Session- und Memory-Pfad-Hilfsfunktionen

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 8: Health Monitor

**Files:**
- Create: `sigoengine/channel_health.go`

**Interfaces:**
- Consumes: `ChannelRegistry`, `ChannelManager`, `ProbeProvider`
- Produces: `StartHealthMonitor(ctx, manager, interval)`

- [ ] **Step 1: Create `sigoengine/channel_health.go`**

```go
//**********************************************************************
//      sigoengine/channel_health.go
//**********************************************************************
//  Beschreibung: Hintergrund-Health-Monitor für Kanäle
//**********************************************************************

package sigoengine

import (
	"context"
	"time"
)

// StartHealthMonitor starts a goroutine that periodically health-checks all
// active channels. It activates inactive reserve channels when all active
// channels for a provider become unhealthy. It disables channels on auth errors.
func StartHealthMonitor(ctx context.Context, manager *ChannelManager, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runHealthChecks(manager)
			}
		}
	}()
}

func runHealthChecks(manager *ChannelManager) {
	registry := manager.Registry()
	for _, provider := range registry.AllProviders() {
		allActiveUnhealthy := true
		hasActive := false
		var firstInactive *Channel

		for _, ch := range registry.Channels(provider) {
			if !ch.Active {
				if firstInactive == nil {
					firstInactive = ch
				}
				continue
			}
			hasActive = true
			checkChannel(ch)
			if ch.Healthy {
				allActiveUnhealthy = false
			}
		}

		if hasActive && allActiveUnhealthy && firstInactive != nil {
			LogInfo("Auto-enabling reserve channel", map[string]interface{}{
				"provider": provider,
				"channel":  firstInactive.Name,
			})
			registry.SetActive(provider, firstInactive.Name, true)
		}
	}
}

func checkChannel(ch *Channel) {
	// Build a minimal config for the provider endpoint.
	// We do not know the exact model here; use the provider name as a placeholder
	// and the channel API key. ProbeProvider sends a tiny request.
	m, _ := GetModelByID(ResolveModelName(ch.Provider))
	endpoint := m.Endpoint
	if endpoint == "" {
		endpoint = "https://api.mammouth.ai/v1/chat/completions"
	}

	cfg := &ProviderConfig{
		Endpoint: endpoint,
		Model:    ch.Provider,
		APIKey:   ch.APIKey,
		Type:     "mammoth",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	health := ProbeProvider(ctx, cfg)
	ch.LastHealthCheck = time.Now()
	if health.Status == "available" {
		ch.Healthy = true
		ch.LastError = ""
		ch.ConsecutiveErrors = 0
		return
	}

	ch.Healthy = false
	ch.LastError = health.Error
	ch.ConsecutiveErrors++

	apiErr := ClassifyError(NewError(health.Error, health.Error, nil, nil))
	if apiErr.Type == ErrAuthFailed {
		LogWarn("Disabling channel due to auth failure", map[string]interface{}{
			"provider": ch.Provider,
			"channel":  ch.Name,
		})
		ch.Active = false
	}
}
```

Note: The placeholder endpoint logic is weak. A better approach is to discover the endpoint from the first model for that provider. This will be refined in Task 10. For now, the monitor runs but may probe with an incorrect endpoint for non-Mammoth providers. Mark this as a known limitation to fix in Task 10.

- [ ] **Step 2: Build**

Run:
```bash
go build ./sigoengine/
```

Expected: success (with known endpoint limitation).

- [ ] **Step 3: Commit**

```bash
git add sigoengine/channel_health.go
git commit -m "feat: Health-Monitor für Kanäle

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 9: REST `/api/version` Endpoint

**Files:**
- Modify: `sigoREST/main.go`

**Interfaces:**
- Consumes: `sigoengine.Version`
- Produces: `handleVersion` HTTP handler

- [ ] **Step 1: Add handler to `sigoREST/main.go`**

Add after `handlePing`:

```go
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
```

- [ ] **Step 2: Register route**

In `main()`, add:

```go
mux.HandleFunc("/api/version", srv.handleVersion)
```

- [ ] **Step 3: Build and manual test**

Run:
```bash
go build -o sigoREST/sigoREST ./sigoREST/
./sigoREST/sigoREST -v debug &
sleep 2
curl -s http://localhost:9080/api/version
kill %1
```

Expected output:
```json
{"component":"sigoREST","version":"1.1"}
```

- [ ] **Step 4: Commit**

```bash
git add sigoREST/main.go
git commit -m "feat: REST Endpunkt /api/version

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 10: REST Channels API

**Files:**
- Modify: `sigoREST/main.go`

**Interfaces:**
- Consumes: `Server.channelManager`, `ChannelManager`, `ChannelRegistry`
- Produces: handlers for `/api/channels`, `/api/channels/:provider/:name`, enable/disable, memory, system-prompt

- [ ] **Step 1: Add channel manager to Server struct**

In `sigoREST/main.go`, add to `Server`:

```go
type Server struct {
	mu             sync.RWMutex
	memory         MemoryBlock
	models         map[string]ModelInfo
	breakers       map[string]*sigoengine.EnhancedCircuitBreaker
	systemPrompt   string
	usageMu        sync.RWMutex
	usage          map[string]*ModelUsageStats
	channelManager *sigoengine.ChannelManager
	baseDir        string
}
```

- [ ] **Step 2: Initialize channel manager in main()**

After `srv := &Server{...}`, add:

```go
baseDir := "/var/sigoREST"
if _, err := os.Stat(baseDir); os.IsNotExist(err) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		sigoengine.LogWarn("Konnte /var/sigoREST nicht anlegen", map[string]interface{}{"error": err.Error()})
	}
}
registry := sigoengine.NewChannelRegistry(filepath.Join(baseDir, "channels.json"))
registry.DiscoverFromEnv()
if err := registry.LoadState(); err != nil {
	sigoengine.LogWarn("Kanal-Status konnte nicht geladen werden", map[string]interface{}{"error": err.Error()})
}
srv.channelManager = sigoengine.NewChannelManager(registry)
srv.baseDir = baseDir
```

Also add `import "path/filepath"` if not already present.

- [ ] **Step 3: Start health monitor**

After channel manager initialization, add:

```go
channelHealthInterval := 30 * time.Second
sigoengine.StartHealthMonitor(context.Background(), srv.channelManager, channelHealthInterval)
```

- [ ] **Step 4: Add handlers**

Add to `sigoREST/main.go`:

```go
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
func (s *Server) handleChannelDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provider, name := extractChannelParams(r.URL.Path)
	ch, ok := s.channelManager.Registry().GetChannel(provider, name)
	if !ok {
		writeError(w, "Channel not found", "not_found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"provider":            ch.Provider,
		"name":                ch.Name,
		"full_name":           ch.FullName(),
		"active":              ch.Active,
		"healthy":             ch.Healthy,
		"last_health_check":   ch.LastHealthCheck,
		"last_error":          ch.LastError,
		"consecutive_errors":  ch.ConsecutiveErrors,
	})
}

// POST /api/channels/:provider/:name/enable
func (s *Server) handleChannelEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provider, name := extractChannelParams(r.URL.Path)
	if err := s.channelManager.Registry().SetActive(provider, name, true); err != nil {
		writeError(w, err.Error(), "not_found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "enabled"})
}

// POST /api/channels/:provider/:name/disable
func (s *Server) handleChannelDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provider, name := extractChannelParams(r.URL.Path)
	if err := s.channelManager.Registry().SetActive(provider, name, false); err != nil {
		writeError(w, err.Error(), "not_found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "disabled"})
}

func extractChannelParams(path string) (provider, name string) {
	// Expected: /api/channels/<provider>/<name>/...
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 4 {
		provider = parts[2]
		name = parts[3]
	}
	return
}
```

- [ ] **Step 5: Register routes**

In `main()`, add:

```go
mux.HandleFunc("/api/channels/", srv.handleChannelDetail)
// Order matters: more specific paths first
mux.HandleFunc("/api/channels", srv.handleChannels)
```

Wait, the current routing with `http.ServeMux` does not support path parameters. We need a small router or path-based dispatch. Simpler: register one handler `/api/channels/` and dispatch inside based on path segments.

Replace the two route registrations with:

```go
mux.HandleFunc("/api/channels/", srv.handleChannelRouter)
mux.HandleFunc("/api/channels", srv.handleChannels)
```

Add `handleChannelRouter`:

```go
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
		if action == "" {
			s.handleChannelDetail(w, r)
			return
		}
	case http.MethodPost:
		switch action {
		case "enable":
			s.handleChannelEnable(w, r)
			return
		case "disable":
			s.handleChannelDisable(w, r)
			return
		}
	}
	writeError(w, "Invalid channel operation", "invalid_request", http.StatusBadRequest)
}
```

Also update `handleChannelDetail/Enable/Disable` to not parse the path themselves; instead receive provider/name from the router. Refactor them to accept provider/name or read from request context. Simplest: keep the extraction functions but have the router set them on the request context. To avoid complexity, change signatures to accept provider/name and have the router call them directly.

Final handlers (private, with params):

```go
func (s *Server) handleChannelDetail(w http.ResponseWriter, r *http.Request, provider, name string) {
	ch, ok := s.channelManager.Registry().GetChannel(provider, name)
	...
}
```

Router calls them.

- [ ] **Step 6: Channel memory and system-prompt handlers**

Add within `handleChannelRouter`:

```go
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
```

Add handlers:

```go
func (s *Server) handleChannelMemoryGet(w http.ResponseWriter, r *http.Request, provider, name string) {
	path := sigoengine.ChannelMemoryPath(s.baseDir, provider, name)
	data, err := os.ReadFile(path)
	if err != nil {
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
```

- [ ] **Step 7: Build**

Run:
```bash
go build ./sigoREST/
```

Expected: success.

- [ ] **Step 8: Commit**

```bash
git add sigoREST/main.go
git commit -m "feat: REST API für Kanäle (/api/channels/*)

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 11: Integrate Channels into Chat Completions

**Files:**
- Modify: `sigoREST/main.go`

**Interfaces:**
- Consumes: `ChannelManager.Resolve`, `LoadConfigWithChannel`, per-channel session/memory helpers
- Produces: updated `handleChatCompletions`

- [ ] **Step 1: Add provider detection helper**

In `sigoREST/main.go`, add:

```go
// providerForModel returns the provider name for a given model ID/shortcode.
// It uses the model registry first, then falls back to heuristics.
func (s *Server) providerForModel(modelID string) string {
	s.mu.RLock()
	info, ok := s.models[modelID]
	s.mu.RUnlock()
	if ok && info.APIKey != "" {
		// Map APIKey env var name to provider
		switch {
		case strings.HasPrefix(info.APIKey, "MAMMOUTH"):
			return "mammouth"
		case strings.HasPrefix(info.APIKey, "MOONSHOT"):
			return "moonshot"
		case strings.HasPrefix(info.APIKey, "ZAI"):
			return "zai"
		}
	}
	// Fallback by shortcode/model name heuristics
	switch {
	case strings.Contains(modelID, "kimi"):
		return "moonshot"
	case strings.Contains(modelID, "GLM"):
		return "zai"
	default:
		return "mammouth"
	}
}
```

- [ ] **Step 2: Update ChatRequest struct**

Add to `ChatRequest`:

```go
Channel string `json:"channel,omitempty"`
```

- [ ] **Step 3: Refactor handleChatCompletions to use channels**

This is the largest change. After model validation, add channel resolution:

```go
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
```

Replace the existing `ProviderConfig` construction:

```go
cfg := &sigoengine.ProviderConfig{
    Endpoint: modelInfo.Endpoint,
    Model:    modelID,
    APIKey:   os.Getenv(modelInfo.APIKey),
}
```

with:

```go
cfg, err := sigoengine.LoadConfigWithChannel(modelID, ch)
if err != nil {
    writeError(w, err.Error(), "config_error", http.StatusInternalServerError)
    return
}
// Override endpoint from modelInfo to ensure correct provider endpoint
cfg.Endpoint = modelInfo.Endpoint
```

- [ ] **Step 4: Per-channel memory and sessions**

When building messages, load channel memory:

```go
memPath := sigoengine.ChannelMemoryPath(s.baseDir, ch.Provider, ch.Name)
if data, err := os.ReadFile(memPath); err == nil {
    var mem sigoengine.MemoryBlock
    if err := json.Unmarshal(data, &mem); err == nil && mem.Content != "" {
        messages = append(messages, map[string]interface{}{
            "role": "system", "content": mem.Content,
        })
    }
}
```

For sessions:

```go
if req.SessionID != "" {
    session := sigoengine.LoadSessionForChannel(s.baseDir, ch.Provider, ch.Name, req.SessionID, req.Model)
    ...
}
```

And save:

```go
if req.SessionID != "" && userPrompt != "" {
    session := sigoengine.LoadSessionForChannel(s.baseDir, ch.Provider, ch.Name, req.SessionID, req.Model)
    session.AddMessage("user", userPrompt)
    session.AddMessage("assistant", responseText)
    session.SaveForChannel(s.baseDir, ch.Provider, ch.Name, req.SessionID, req.Model)
}
```

- [ ] **Step 5: Failover logic in chat handler**

Wrap the existing `RetryWithBackoff` call in a channel loop:

```go
var responseText string
var responseUsage *sigoengine.UsageData
var responseFinishReason string

channels := []*sigoengine.Channel{ch}
for {
	if next, ok := s.channelManager.NextActive(provider, ch); ok {
		channels = append(channels, next)
		ch = next
	} else {
		break
	}
}

var lastErr error
for _, currentCh := range channels {
	cfg, err := sigoengine.LoadConfigWithChannel(modelID, currentCh)
	if err != nil {
		lastErr = err
		continue
	}
	cfg.Endpoint = modelInfo.Endpoint

	// Circuit breaker key: model#channel
	cbKey := fmt.Sprintf("%s#%s", req.Model, currentCh.FullName())
	s.mu.Lock()
	if _, exists := s.breakers[cbKey]; !exists {
		s.breakers[cbKey] = sigoengine.NewEnhancedCircuitBreaker(nil)
	}
	breaker := s.breakers[cbKey]
	s.mu.Unlock()

	lastErr = sigoengine.RetryWithBackoff(ctx, retryConfig, func() error {
		return breaker.Do(func() error {
			text, u, fr, e := sigoengine.CallAPI(ctx, cfg, apiRequest, req.Timeout)
			if e != nil {
				apiErr := sigoengine.ClassifyError(e)
				if apiErr.Type == sigoengine.ErrAuthFailed {
					currentCh.Active = false
				}
				return e
			}
			responseText = text
			responseUsage = u
			responseFinishReason = fr
			return nil
		})
	})

	if lastErr == nil {
		break
	}

	apiErr := sigoengine.ClassifyError(lastErr)
	if apiErr.Type == sigoengine.ErrClientError {
		break
	}
	LogWarn("Failing over to next channel", map[string]interface{}{
		"model":     req.Model,
		"channel":   currentCh.FullName(),
		"error_type": apiErr.Type,
	})
}

if lastErr != nil {
    // existing error handling
}
```

- [ ] **Step 6: Build**

Run:
```bash
go build ./sigoREST/
```

Expected: success.

- [ ] **Step 7: Commit**

```bash
git add sigoREST/main.go
git commit -m "feat: Kanal-Auflösung und Failover in Chat-Completions

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 12: CLI Flags for Channel and Session Directory

**Files:**
- Modify: `cmd/sigoE/main.go`

**Interfaces:**
- Consumes: `sigoengine.ChannelManager`, `LoadConfigWithChannel`, `SessionPath`, `DefaultSessionBaseDir`
- Produces: `-c <channel>`, `-session-dir <path>`, `-version` already done

- [ ] **Step 1: Add CLI flags**

In `cmd/sigoE/main.go`:

```go
channelFlag := flag.String("c", "", "Kanal wählen (z.B. mammouth-0)")
sessionDirFlag := flag.String("session-dir", sigoengine.DefaultSessionBaseDir, "Verzeichnis für Sessions")
```

- [ ] **Step 2: Build a minimal channel manager for CLI**

After `flag.Parse()` and before the main logic:

```go
registry := sigoengine.NewChannelRegistry("")
registry.DiscoverFromEnv()
channelManager := sigoengine.NewChannelManager(registry)
```

- [ ] **Step 3: Use channel manager when loading config**

Where `cfg, err := sigoengine.LoadConfig(*modelFlag)` is called, replace with:

```go
provider := providerForModelCLI(*modelFlag)
ch, err := channelManager.Resolve(provider, *channelFlag)
if err != nil {
    fmt.Fprintf(os.Stderr, "Fehler: %v\n", err)
    os.Exit(1)
}
cfg, err := sigoengine.LoadConfigWithChannel(*modelFlag, ch)
if err != nil {
    fmt.Fprintf(os.Stderr, "Fehler: %v\n", err)
    os.Exit(1)
}
```

Add helper:

```go
func providerForModelCLI(model string) string {
	id := sigoengine.ResolveModelName(model)
	if m, ok := sigoengine.GetModelByID(id); ok {
		switch {
		case strings.HasPrefix(m.APIKeyEnv, "MAMMOUTH"):
			return "mammouth"
		case strings.HasPrefix(m.APIKeyEnv, "MOONSHOT"):
			return "moonshot"
		case strings.HasPrefix(m.APIKeyEnv, "ZAI"):
			return "zai"
		}
	}
	return "mammouth"
}
```

- [ ] **Step 4: Use session directory and channel in session calls**

Where `sigoengine.LoadSession` is called:

```go
session := sigoengine.LoadSessionForChannel(*sessionDirFlag, ch.Provider, ch.Name, *sessionFlag, *modelFlag)
```

Where `session.Save(...)` is called:

```go
session.SaveForChannel(*sessionDirFlag, ch.Provider, ch.Name, *sessionFlag, *modelFlag)
```

- [ ] **Step 5: Build and manual test**

Run:
```bash
go build -o sigoE ./cmd/sigoE/
MAMMOUTH_API_KEY=dummy MAMMOUTH_API_KEY_0=dummy2 ./sigoE -version
```

Expected output:
```
sigoE 1.1
```

- [ ] **Step 6: Commit**

```bash
git add cmd/sigoE/main.go
git commit -m "feat: CLI Flags -c und -session-dir für Kanal-Support

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 13: Unit Tests for Channel Registry

**Files:**
- Create: `sigoengine/channel_test.go`

**Interfaces:**
- Consumes: `ChannelRegistry`, `DiscoverFromEnv`

- [ ] **Step 1: Create test file**

```go
//**********************************************************************
//      sigoengine/channel_test.go
//**********************************************************************

package sigoengine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChannelRegistry_DiscoverFromEnv(t *testing.T) {
	os.Setenv("MAMMOUTH_API_KEY", "default-key")
	os.Setenv("MAMMOUTH_API_KEY_0", "key-0")
	os.Setenv("MAMMOUTH_API_KEY_1", "key-1")
	os.Setenv("MOONSHOT_API_KEY", "moon-default")
	defer func() {
		os.Unsetenv("MAMMOUTH_API_KEY")
		os.Unsetenv("MAMMOUTH_API_KEY_0")
		os.Unsetenv("MAMMOUTH_API_KEY_1")
		os.Unsetenv("MOONSHOT_API_KEY")
	}()

	reg := NewChannelRegistry("")
	reg.DiscoverFromEnv()

	mammoth := reg.Channels("mammouth")
	if len(mammoth) != 3 {
		t.Fatalf("expected 3 mammouth channels, got %d", len(mammoth))
	}
	if mammoth[0].Name != "default" || !mammoth[0].Active {
		t.Errorf("default channel should be active, got %+v", mammoth[0])
	}
	if mammoth[1].Name != "0" || mammoth[1].Active {
		t.Errorf("channel 0 should be inactive, got %+v", mammoth[1])
	}
	if mammoth[1].APIKey != "key-0" {
		t.Errorf("channel 0 key mismatch: %q", mammoth[1].APIKey)
	}

	moon := reg.Channels("moonshot")
	if len(moon) != 1 {
		t.Fatalf("expected 1 moonshot channel, got %d", len(moon))
	}
}

func TestChannelRegistry_LoadSaveState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "channels.json")

	os.Setenv("MAMMOUTH_API_KEY", "default-key")
	os.Setenv("MAMMOUTH_API_KEY_0", "key-0")
	defer func() {
		os.Unsetenv("MAMMOUTH_API_KEY")
		os.Unsetenv("MAMMOUTH_API_KEY_0")
	}()

	reg := NewChannelRegistry(statePath)
	reg.DiscoverFromEnv()
	if err := reg.SetActive("mammouth", "0", true); err != nil {
		t.Fatalf("SetActive failed: %v", err)
	}

	reg2 := NewChannelRegistry(statePath)
	reg2.DiscoverFromEnv()
	if err := reg2.LoadState(); err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}

	ch, ok := reg2.GetChannel("mammouth", "0")
	if !ok || !ch.Active {
		t.Errorf("expected channel 0 to be active after loading state")
	}
}
```

- [ ] **Step 2: Run tests**

Run:
```bash
go test ./sigoengine/ -run TestChannelRegistry -v
```

Expected: PASS for both tests.

- [ ] **Step 3: Commit**

```bash
git add sigoengine/channel_test.go
git commit -m "test: Unit-Tests für ChannelRegistry

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 14: Unit Tests for Channel Manager

**Files:**
- Create: `sigoengine/channel_manager_test.go`

**Interfaces:**
- Consumes: `ChannelManager`, `ChannelRegistry`

- [ ] **Step 1: Create test file**

```go
//**********************************************************************
//      sigoengine/channel_manager_test.go
//**********************************************************************

package sigoengine

import (
	"os"
	"testing"
)

func TestChannelManager_Resolve(t *testing.T) {
	os.Setenv("MAMMOUTH_API_KEY", "default-key")
	os.Setenv("MAMMOUTH_API_KEY_0", "key-0")
	defer func() {
		os.Unsetenv("MAMMOUTH_API_KEY")
		os.Unsetenv("MAMMOUTH_API_KEY_0")
	}()

	reg := NewChannelRegistry("")
	reg.DiscoverFromEnv()
	mgr := NewChannelManager(reg)

	ch, err := mgr.Resolve("mammouth", "")
	if err != nil || ch.Name != "default" {
		t.Fatalf("expected default channel, got %+v, err=%v", ch, err)
	}

	_, err = mgr.Resolve("mammouth", "0")
	if err == nil {
		t.Fatal("expected error for inactive channel 0")
	}

	reg.SetActive("mammouth", "0", true)
	ch, err = mgr.Resolve("mammouth", "0")
	if err != nil || ch.Name != "0" {
		t.Fatalf("expected channel 0, got %+v, err=%v", ch, err)
	}
}

func TestChannelManager_NextActive(t *testing.T) {
	os.Setenv("MAMMOUTH_API_KEY", "default-key")
	os.Setenv("MAMMOUTH_API_KEY_0", "key-0")
	os.Setenv("MAMMOUTH_API_KEY_1", "key-1")
	defer func() {
		os.Unsetenv("MAMMOUTH_API_KEY")
		os.Unsetenv("MAMMOUTH_API_KEY_0")
		os.Unsetenv("MAMMOUTH_API_KEY_1")
	}()

	reg := NewChannelRegistry("")
	reg.DiscoverFromEnv()
	reg.SetActive("mammouth", "0", true)
	reg.SetActive("mammouth", "1", true)
	mgr := NewChannelManager(reg)

	def, _ := reg.GetChannel("mammouth", "default")
	ch0, _ := reg.GetChannel("mammouth", "0")
	ch1, _ := reg.GetChannel("mammouth", "1")

	next, ok := mgr.NextActive("mammouth", def)
	if !ok || next.Name != "0" {
		t.Fatalf("expected next after default to be 0, got %+v", next)
	}

	next, ok = mgr.NextActive("mammouth", ch0)
	if !ok || next.Name != "1" {
		t.Fatalf("expected next after 0 to be 1, got %+v", next)
	}

	_, ok = mgr.NextActive("mammouth", ch1)
	if ok {
		t.Fatal("expected no next channel after 1")
	}
}
```

- [ ] **Step 2: Run tests**

Run:
```bash
go test ./sigoengine/ -run TestChannelManager -v
```

Expected: PASS for both tests.

- [ ] **Step 3: Commit**

```bash
git add sigoengine/channel_manager_test.go
git commit -m "test: Unit-Tests für ChannelManager

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 15: Update systemd Documentation

**Files:**
- Modify: `docs/systemd-install.md`

**Interfaces:**
- Produces: instructions for `/var/sigoREST` permissions

- [ ] **Step 1: Add /var/sigoREST setup section**

Append to `docs/systemd-install.md`:

```markdown
## /var/sigoREST Verzeichnis

Der Server speichert pro-Kanal Sessions, Memory und System-Prompts unter `/var/sigoREST`.

```bash
sudo mkdir -p /var/sigoREST
sudo chown -R sigorest:sigorest /var/sigoREST
sudo chmod 0755 /var/sigoREST
```

Im Service-File muss der Benutzer Schreibrechte haben:

```ini
[Service]
User=sigorest
Group=sigorest
```
```

- [ ] **Step 2: Commit**

```bash
git add docs/systemd-install.md
git commit -m "docs: /var/sigoREST Setup für systemd

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Self-Review

### Spec Coverage Check

| Spec Requirement | Implementing Task |
|---|---|
| Mehrere API-Keys pro Provider | Task 3 |
| Default-Kanal aktiv | Task 3 |
| Manuelle Aktivierung (Env + API) | Task 3, Task 10 |
| Automatisches Failover | Task 5, Task 11 |
| Auto-Aktivierung via Health-Monitor | Task 8 |
| Eigener Memory/Sessions pro Kanal | Task 7, Task 11 |
| OpenAI-Kompatibilität erhalten | Task 11 (no model list changes) |
| CLI `-c` / `-session-dir` | Task 12 |
| Versions-String CLI + REST | Task 1, Task 9 |
| `/var/sigoREST` Persistenz | Task 7, Task 15 |

No gaps identified.

### Placeholder Scan

Checked for:
- TBD/TODO — none
- "Add appropriate error handling" — all error handling is explicit in code
- "Write tests for the above" — tests have concrete code
- "Similar to Task N" — repeated where needed for standalone readability

All steps contain actual code or exact commands.

### Type Consistency Check

- `Channel.FullName()` used in Task 5 and Task 11.
- `ChannelManager.Resolve(provider, requested string)` consistent across Task 5, Task 11, Task 12.
- `LoadConfigWithChannel(model string, ch *Channel)` consistent across Task 6, Task 11, Task 12.
- `SessionPath(baseDir, provider, channel, model, sessionID)` consistent across Task 7, Task 11, Task 12.
- `Server.baseDir` string used consistently in Task 10 and Task 11.

No mismatches found.

### Known Limitations to Address During Execution

1. **Task 8 endpoint detection:** `checkChannel` uses a placeholder endpoint. During Task 10/11 implementation, ensure the health monitor probes with the correct provider endpoint by looking up the first model for that provider from the model registry, or pass the endpoint into the channel/registry.
2. **Channel memory precedence:** The design says global memory is used if channel memory is empty. During Task 11, keep the existing global memory behavior as fallback when channel memory file does not exist.
3. **CLI auto-fallback:** The CLI does not implement failover across channels in this plan; it resolves one channel and uses it. If the user wants CLI failover too, add it as a follow-up task.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-06-17-multi-channel.md`.**

Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using `superpowers:executing-plans`, batch execution with checkpoints.

Which approach?
