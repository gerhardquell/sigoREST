# Langzeitstabile ID-/Shortcode-Registry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Shortcodes bleiben über Server-Neustarts und Provider-Ausfälle hinweg stabil (assign-once, nie wiederverwendet), zeigen Provider + Modell, und Aufrufe auf inzwischen abgeschaltete Modelle liefern einen klaren Fehler statt eines stillen Fallbacks auf ein anderes Modell.

**Architecture:** Neue persistente SQLite-Registry (`sigoengine/id_registry.go`, gleicher Treiber/gleiches Muster wie `costdb.go`) vergibt 3-Zeichen-Provider-Kürzel und semantische Modell-Shortcodes einmalig und dauerhaft. `loadModelsFromProviders()` in `sigoREST/main.go` synchronisiert nach jedem erfolgreichen Provider-Fetch die Registry und übernimmt deren Shortcode statt des bisherigen pro-Boot berechneten. `lookupModel()` löst zusätzlich Shortcodes mit Kanal-Suffix auf und erkennt retired Modelle; `handleChatCompletions`/`handleMessages` lehnen retired Modelle mit HTTP 410 ab.

**Tech Stack:** Go 1.26, `modernc.org/sqlite` (bereits Projektabhängigkeit über `costdb.go`)

**Spec:** `docs/superpowers/specs/2026-09-20-id-shortcode-registry-design.md`

## Global Constraints

- Provider-Kürzel: genau 3 Zeichen, `a-z`, assign-once, nie wiederverwendet.
- Modell-Shortcode-Format: `{provider3}-{semanticModelCode}` (ohne Kanal-Suffix in der Registry gespeichert; Kanal-Suffix wird nur bei der Auflösung einer Anfrage geparst, nicht persistiert).
- Retire-Schwelle: 3 aufeinanderfolgende **erfolgreiche** Provider-Fetches, bei denen das Modell fehlt.
- Ein fehlgeschlagener Provider-Fetch darf niemals `miss_streak` erhöhen (schützt vor der dokumentierten ZAI/Longcat-Fallback-Asymmetrie).
- Retired Modelle werden nie gelöscht und ihr Shortcode nie wiederverwendet; taucht dasselbe `(provider, upstream_id)` wieder auf, wird derselbe Shortcode reaktiviert.
- Retired-Aufruf → HTTP 410, klare Fehlermeldung, kein automatischer Fallback auf ein anderes Modell.
- Alle neuen SQLite-Zugriffe laufen über `modernc.org/sqlite` (pure Go, kein CGO) — kein neuer Treiber.
- Bestehendes Verhalten bei Registry-Öffnungsfehler: nil-safe, Server läuft weiter (Shortcodes dann wieder pro Boot berechnet, wie bisher) — analog zu `costDB`.

---

### Task 1: SQLite-Schema + Open/Close (`IDRegistry`-Grundgerüst)

**Files:**
- Create: `sigoengine/id_registry.go`
- Create: `sigoengine/id_registry_test.go`

**Interfaces:**
- Produces: `type ModelEntry struct { Provider, UpstreamID, Shortcode string; AssignedAt time.Time; RetiredAt *time.Time; MissStreak int }`
- Produces: `type IDRegistry struct { ... }` (unexported Felder)
- Produces: `func OpenIDRegistry(dataDir string) (*IDRegistry, error)`
- Produces: `func (r *IDRegistry) Close() error`
- Produces: `func (r *IDRegistry) Path() string`

- [ ] **Step 1: Failing Test schreiben**

```go
// sigoengine/id_registry_test.go
package sigoengine

import (
	"path/filepath"
	"testing"
)

func newTestIDRegistry(t *testing.T) *IDRegistry {
	t.Helper()
	dir := t.TempDir()
	r, err := OpenIDRegistry(dir)
	if err != nil {
		t.Fatalf("OpenIDRegistry: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestOpenIDRegistry_CreatesFileAndSchema(t *testing.T) {
	dir := t.TempDir()
	r, err := OpenIDRegistry(dir)
	if err != nil {
		t.Fatalf("OpenIDRegistry: %v", err)
	}
	defer r.Close()

	wantPath := filepath.Join(dir, "id_registry.db")
	if r.Path() != wantPath {
		t.Errorf("Path() = %q, erwartet %q", r.Path(), wantPath)
	}

	// Schema-Test: providers/models-Tabellen müssen ohne Fehler abfragbar sein.
	if _, err := r.providerCodeLocked("mammouth"); err != nil {
		t.Fatalf("providers-Tabelle nicht nutzbar: %v", err)
	}
	if _, err := r.getModelLocked("mammouth", "gpt-4.1"); err != nil {
		t.Fatalf("models-Tabelle nicht nutzbar: %v", err)
	}
}
```

- [ ] **Step 2: Test ausführen, Fehlschlag bestätigen**

Run: `go test ./sigoengine/... -run TestOpenIDRegistry_CreatesFileAndSchema -v`
Expected: FAIL — `OpenIDRegistry`/`ModelEntry` existieren nicht.

- [ ] **Step 3: `sigoengine/id_registry.go` anlegen**

```go
//**********************************************************************
//      sigoengine/id_registry.go
//**********************************************************************
//  Beschreibung: Persistente Registry für Provider-Kürzel und
//  Modell-Shortcodes. Löst das Dynamisierungs-Problem des ursprünglich
//  in TODO.md vorgeschlagenen Positions-Shortcodes (p1m1c0): sigoREST
//  lädt Modelle bei jedem Boot dynamisch von Live-Provider-APIs, eine
//  bei jedem Boot neu berechnete Position würde sich bei jeder Änderung
//  der Live-Liste verschieben. Diese Registry vergibt Kürzel einmalig
//  (assign-once) und persistiert sie in SQLite (gleicher Treiber wie
//  costdb.go: modernc.org/sqlite, pure Go, kein CGO).
//
//  Siehe docs/superpowers/specs/2026-09-20-id-shortcode-registry-design.md
//**********************************************************************

package sigoengine

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// **********************************************************************
// Typen

// ModelEntry ist ein persistenter Registry-Eintrag für ein Modell.
type ModelEntry struct {
	Provider   string
	UpstreamID string
	Shortcode  string
	AssignedAt time.Time
	RetiredAt  *time.Time // nil = aktiv
	MissStreak int
}

// IDRegistry kapselt die SQLite-Verbindung für Provider-/Modell-Kürzel.
type IDRegistry struct {
	db   *sql.DB
	path string
	mu   sync.Mutex
}

// ErrShortcodeNotFound signalisiert, dass ein Shortcode (auch mit
// Kanal-Suffix) in der Registry nicht gefunden wurde.
var ErrShortcodeNotFound = errors.New("id_registry: shortcode not found")

// retireThreshold: so viele aufeinanderfolgende ERFOLGREICHE Fetches
// muss ein Modell fehlen, bevor es als retired markiert wird.
const retireThreshold = 3

// **********************************************************************
// Öffnen / Schema

// OpenIDRegistry öffnet (oder erstellt) <dataDir>/id_registry.db.
func OpenIDRegistry(dataDir string) (*IDRegistry, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("id_registry: data-dir anlegen fehlgeschlagen: %w", err)
	}
	path := filepath.Join(dataDir, "id_registry.db")

	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("id_registry: öffnen fehlgeschlagen: %w", err)
	}
	db.SetMaxOpenConns(4)

	r := &IDRegistry{db: db, path: path}
	if err := r.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return r, nil
}

// Close schließt die zugrundeliegende Verbindung.
func (r *IDRegistry) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

// Path gibt den absoluten Pfad der DB-Datei zurück (für Logging).
func (r *IDRegistry) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

func (r *IDRegistry) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS providers (
			name        TEXT PRIMARY KEY,
			code        TEXT UNIQUE NOT NULL,
			assigned_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS models (
			provider     TEXT NOT NULL,
			upstream_id  TEXT NOT NULL,
			shortcode    TEXT UNIQUE NOT NULL,
			assigned_at  INTEGER NOT NULL,
			retired_at   INTEGER,
			miss_streak  INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (provider, upstream_id)
		)`,
	}
	for _, s := range stmts {
		if _, err := r.db.Exec(s); err != nil {
			return fmt.Errorf("id_registry: migration fehlgeschlagen (%q): %w", s, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Platzhalter für in Task 2/3 implementierte Methoden ergänzen**

Damit Step 1's Test kompiliert, vorerst minimale Implementierungen anhängen (werden in Task 2 vollständig ersetzt):

```go
func (r *IDRegistry) providerCodeLocked(provider string) (string, error) {
	var code string
	err := r.db.QueryRow(`SELECT code FROM providers WHERE name = ?`, provider).Scan(&code)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("id_registry: provider-code lesen fehlgeschlagen: %w", err)
	}
	return code, nil
}

func (r *IDRegistry) getModelLocked(provider, upstreamID string) (*ModelEntry, error) {
	row := r.db.QueryRow(
		`SELECT shortcode, assigned_at, retired_at, miss_streak FROM models WHERE provider = ? AND upstream_id = ?`,
		provider, upstreamID,
	)
	var sc string
	var assignedAt int64
	var retiredAt sql.NullInt64
	var missStreak int
	err := row.Scan(&sc, &assignedAt, &retiredAt, &missStreak)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("id_registry: modell lesen fehlgeschlagen: %w", err)
	}
	entry := &ModelEntry{Provider: provider, UpstreamID: upstreamID, Shortcode: sc, AssignedAt: time.Unix(assignedAt, 0), MissStreak: missStreak}
	if retiredAt.Valid {
		t := time.Unix(retiredAt.Int64, 0)
		entry.RetiredAt = &t
	}
	return entry, nil
}
```

Diese zwei Methoden reichen genau, damit `TestOpenIDRegistry_CreatesFileAndSchema` grün wird — `strings` wird erst in Task 2 gebraucht, also den Import vorerst weglassen falls der Compiler unused-import meldet (in Task 2 wieder hinzufügen).

- [ ] **Step 5: Test ausführen, Erfolg bestätigen**

Run: `go test ./sigoengine/... -run TestOpenIDRegistry_CreatesFileAndSchema -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add sigoengine/id_registry.go sigoengine/id_registry_test.go
git commit -m "feat: SQLite-Grundgerüst für ID-Registry (Schema, Open/Close)"
```

---

### Task 2: Provider-Kürzel-Vergabe (assign-once + Kollisions-Fallback)

**Files:**
- Modify: `sigoengine/id_registry.go` (ersetzt `providerCodeLocked` aus Task 1)
- Modify: `sigoengine/id_registry_test.go`

**Interfaces:**
- Consumes: `cutterCode(word string) string` (aus `sigoengine/shortcode.go`, bereits im Package vorhanden)
- Produces: `func (r *IDRegistry) providerCodeLocked(provider string) (string, error)` — vergibt/liest 3-Zeichen-Code, legt bei Erstaufruf einen `providers`-Eintrag an.

- [ ] **Step 1: Failing Tests schreiben**

```go
func TestProviderCode_AssignOnceAndStable(t *testing.T) {
	r := newTestIDRegistry(t)

	code1, err := r.providerCodeLocked("mammouth")
	if err != nil {
		t.Fatalf("providerCodeLocked: %v", err)
	}
	if len(code1) != 3 {
		t.Fatalf("Code %q hat nicht 3 Zeichen", code1)
	}

	code2, err := r.providerCodeLocked("mammouth")
	if err != nil {
		t.Fatalf("providerCodeLocked (2. Aufruf): %v", err)
	}
	if code1 != code2 {
		t.Errorf("Code änderte sich zwischen Aufrufen: %q -> %q", code1, code2)
	}
}

func TestProviderCode_DifferentProvidersGetDifferentCodes(t *testing.T) {
	r := newTestIDRegistry(t)

	codeA, err := r.providerCodeLocked("zai")
	if err != nil {
		t.Fatalf("providerCodeLocked(zai): %v", err)
	}
	codeB, err := r.providerCodeLocked("longcat")
	if err != nil {
		t.Fatalf("providerCodeLocked(longcat): %v", err)
	}
	if codeA == codeB {
		t.Errorf("zai und longcat bekamen denselben Code %q", codeA)
	}
}

func TestProviderCode_CollisionUsesCutterFallback(t *testing.T) {
	r := newTestIDRegistry(t)

	// Zwei künstliche Provider-Namen mit identischem 3-Buchstaben-Präfix
	// erzwingen den Cutter-Sanborn-Fallback für den zweiten.
	codeA, err := r.providerCodeLocked("mamprovider")
	if err != nil {
		t.Fatalf("providerCodeLocked(mamprovider): %v", err)
	}
	codeB, err := r.providerCodeLocked("mamotherprovider")
	if err != nil {
		t.Fatalf("providerCodeLocked(mamotherprovider): %v", err)
	}
	if codeA == codeB {
		t.Fatalf("Kollision nicht aufgelöst: beide Provider bekamen %q", codeA)
	}
	if len(codeB) != 3 {
		t.Fatalf("Fallback-Code %q hat nicht 3 Zeichen", codeB)
	}
}
```

- [ ] **Step 2: Tests ausführen, Fehlschlag bestätigen**

Run: `go test ./sigoengine/... -run TestProviderCode -v`
Expected: FAIL — `TestProviderCode_AssignOnceAndStable` schlägt fehl, weil die Task-1-Platzhalter-Implementierung nie einen `INSERT` macht (liefert immer leeren String).

- [ ] **Step 3: `providerCodeLocked` vollständig implementieren**

Ersetze die Platzhalter-Funktion aus Task 1 durch:

```go
func (r *IDRegistry) providerCodeLocked(provider string) (string, error) {
	var code string
	err := r.db.QueryRow(`SELECT code FROM providers WHERE name = ?`, provider).Scan(&code)
	if err == nil {
		return code, nil
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("id_registry: provider-code lesen fehlgeschlagen: %w", err)
	}

	candidate := normalizeCode(provider, 3)
	taken, err := r.providerCodeTakenLocked(candidate)
	if err != nil {
		return "", err
	}
	if taken {
		candidate = normalizeCode(cutterCode(provider), 3)
		taken, err = r.providerCodeTakenLocked(candidate)
		if err != nil {
			return "", err
		}
		if taken {
			// Extrem unwahrscheinlicher Doppel-Kollisionsfall: letzte Stelle
			// numerisch durchprobieren.
			base := candidate[:2]
			found := false
			for i := 0; i < 10; i++ {
				alt := fmt.Sprintf("%s%d", base, i)
				altTaken, err := r.providerCodeTakenLocked(alt)
				if err != nil {
					return "", err
				}
				if !altTaken {
					candidate = alt
					found = true
					break
				}
			}
			if !found {
				return "", fmt.Errorf("id_registry: kein freier 3-Zeichen-Code für Provider %q gefunden", provider)
			}
		}
	}

	if _, err := r.db.Exec(
		`INSERT INTO providers (name, code, assigned_at) VALUES (?, ?, ?)`,
		provider, candidate, time.Now().Unix(),
	); err != nil {
		return "", fmt.Errorf("id_registry: provider anlegen fehlgeschlagen: %w", err)
	}
	return candidate, nil
}

func (r *IDRegistry) providerCodeTakenLocked(code string) (bool, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM providers WHERE code = ?`, code).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("id_registry: provider-code-check fehlgeschlagen: %w", err)
	}
	return count > 0, nil
}

// normalizeCode kürzt/füllt s auf genau n Zeichen (lowercase, mit "x"
// aufgefüllt falls zu kurz) — für 3-Zeichen-Provider-Codes.
func normalizeCode(s string, n int) string {
	s = strings.ToLower(s)
	if len(s) >= n {
		return s[:n]
	}
	return s + strings.Repeat("x", n-len(s))
}
```

Import-Block von `sigoengine/id_registry.go` um `"strings"` ergänzen (jetzt gebraucht von `normalizeCode`).

- [ ] **Step 4: Tests ausführen, Erfolg bestätigen**

Run: `go test ./sigoengine/... -run TestProviderCode -v`
Expected: PASS (alle drei Tests)

- [ ] **Step 5: Commit**

```bash
git add sigoengine/id_registry.go sigoengine/id_registry_test.go
git commit -m "feat: Provider-Kürzel assign-once mit Cutter-Sanborn-Kollisionsfallback"
```

---

### Task 3: Modell-Shortcode-Vergabe (`AssignModel`)

**Files:**
- Modify: `sigoengine/id_registry.go` (ersetzt `getModelLocked`-Platzhalter, ergänzt `AssignModel`)
- Modify: `sigoengine/id_registry_test.go`

**Interfaces:**
- Consumes: `GenerateShortcode(modelID string, used map[string]bool) string` (aus `sigoengine/shortcode.go`)
- Consumes: `func (r *IDRegistry) providerCodeLocked(provider string) (string, error)` (Task 2)
- Produces: `func (r *IDRegistry) AssignModel(provider, upstreamID string) (ModelEntry, error)`

- [ ] **Step 1: Failing Tests schreiben**

```go
func TestAssignModel_NewCreatesShortcode(t *testing.T) {
	r := newTestIDRegistry(t)

	entry, err := r.AssignModel("zai", "glm-4.5")
	if err != nil {
		t.Fatalf("AssignModel: %v", err)
	}
	if entry.Shortcode == "" {
		t.Fatal("Shortcode ist leer")
	}
	if entry.RetiredAt != nil {
		t.Error("neues Modell darf nicht retired sein")
	}
	if !strings.HasPrefix(entry.Shortcode, "zai-") {
		t.Errorf("Shortcode %q hat kein Provider-Präfix 'zai-'", entry.Shortcode)
	}
}

func TestAssignModel_SameModelReturnsSameShortcode(t *testing.T) {
	r := newTestIDRegistry(t)

	first, err := r.AssignModel("zai", "glm-4.5")
	if err != nil {
		t.Fatalf("AssignModel (1): %v", err)
	}
	second, err := r.AssignModel("zai", "glm-4.5")
	if err != nil {
		t.Fatalf("AssignModel (2): %v", err)
	}
	if first.Shortcode != second.Shortcode {
		t.Errorf("Shortcode änderte sich: %q -> %q", first.Shortcode, second.Shortcode)
	}
}

func TestAssignModel_CollisionAppendsNumericSuffix(t *testing.T) {
	r := newTestIDRegistry(t)

	// "glm-4.5" und "GLM-4.5" sind unterschiedliche upstream_ids (SQLite-
	// TEXT-Vergleich ist case-sensitiv, kein COLLATE NOCASE im Schema),
	// erzeugen über GenerateShortcode aber garantiert denselben
	// semantischen Code (GenerateShortcode lowercased modelID intern
	// als allerersten Schritt) -- das erzwingt die Kollision deterministisch,
	// ohne auf Zufallstreffer in der Cutter-Sanborn-Tabelle angewiesen zu sein.
	first, err := r.AssignModel("zai", "glm-4.5")
	if err != nil {
		t.Fatalf("AssignModel(glm-4.5): %v", err)
	}
	second, err := r.AssignModel("zai", "GLM-4.5")
	if err != nil {
		t.Fatalf("AssignModel(GLM-4.5): %v", err)
	}
	if first.Shortcode == second.Shortcode {
		t.Fatalf("Kollision nicht aufgelöst: beide Modelle bekamen %q", first.Shortcode)
	}
	wantSecond := first.Shortcode + "-2"
	if second.Shortcode != wantSecond {
		t.Errorf("second.Shortcode = %q, erwartet %q (erste Kollision -> Suffix -2)", second.Shortcode, wantSecond)
	}
}

func TestAssignModel_ReappearedRetiredModelReactivatesSameShortcode(t *testing.T) {
	r := newTestIDRegistry(t)

	entry, err := r.AssignModel("zai", "glm-4.5")
	if err != nil {
		t.Fatalf("AssignModel: %v", err)
	}
	originalShortcode := entry.Shortcode

	// Modell manuell als retired markieren (simuliert das Ergebnis von
	// SyncProvider aus Task 4, die zu diesem Zeitpunkt noch nicht existiert).
	if _, err := r.db.Exec(
		`UPDATE models SET retired_at = ?, miss_streak = ? WHERE provider = ? AND upstream_id = ?`,
		time.Now().Unix(), retireThreshold, "zai", "glm-4.5",
	); err != nil {
		t.Fatalf("retired-Setup fehlgeschlagen: %v", err)
	}

	reactivated, err := r.AssignModel("zai", "glm-4.5")
	if err != nil {
		t.Fatalf("AssignModel (Reaktivierung): %v", err)
	}
	if reactivated.Shortcode != originalShortcode {
		t.Errorf("Shortcode änderte sich bei Reaktivierung: %q -> %q", originalShortcode, reactivated.Shortcode)
	}
	if reactivated.RetiredAt != nil {
		t.Error("reaktiviertes Modell darf nicht mehr retired sein")
	}
	if reactivated.MissStreak != 0 {
		t.Errorf("miss_streak = %d, erwartet 0 nach Reaktivierung", reactivated.MissStreak)
	}
}
```

- [ ] **Step 2: Tests ausführen, Fehlschlag bestätigen**

Run: `go test ./sigoengine/... -run TestAssignModel -v`
Expected: FAIL — `AssignModel` existiert nicht.

- [ ] **Step 3: `AssignModel` implementieren**

In `sigoengine/id_registry.go`, `getModelLocked` aus Task 1 unverändert lassen (wird weiter gebraucht) und ergänzen:

```go
// AssignModel liefert den Registry-Eintrag für (provider, upstreamID):
// existiert er aktiv -> unverändert zurückgeben; existiert er retired ->
// reaktivieren (gleicher Shortcode, miss_streak=0, retired_at=NULL);
// existiert er nicht -> neu anlegen mit einmalig vergebenem Shortcode.
func (r *IDRegistry) AssignModel(provider, upstreamID string) (ModelEntry, error) {
	if r == nil || r.db == nil {
		return ModelEntry{}, fmt.Errorf("id_registry: registry nicht geöffnet")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	providerCode, err := r.providerCodeLocked(provider)
	if err != nil {
		return ModelEntry{}, err
	}

	existing, err := r.getModelLocked(provider, upstreamID)
	if err != nil {
		return ModelEntry{}, err
	}
	if existing != nil {
		if existing.RetiredAt == nil {
			return *existing, nil
		}
		if _, err := r.db.Exec(
			`UPDATE models SET retired_at = NULL, miss_streak = 0 WHERE provider = ? AND upstream_id = ?`,
			provider, upstreamID,
		); err != nil {
			return ModelEntry{}, fmt.Errorf("id_registry: reaktivieren fehlgeschlagen: %w", err)
		}
		existing.RetiredAt = nil
		existing.MissStreak = 0
		return *existing, nil
	}

	semantic := GenerateShortcode(upstreamID, nil)
	base := providerCode + "-" + semantic
	shortcode := base
	for suffix := 2; ; suffix++ {
		taken, err := r.shortcodeTakenLocked(shortcode)
		if err != nil {
			return ModelEntry{}, err
		}
		if !taken {
			break
		}
		shortcode = fmt.Sprintf("%s-%d", base, suffix)
	}

	now := time.Now()
	if _, err := r.db.Exec(
		`INSERT INTO models (provider, upstream_id, shortcode, assigned_at, retired_at, miss_streak)
		 VALUES (?, ?, ?, ?, NULL, 0)`,
		provider, upstreamID, shortcode, now.Unix(),
	); err != nil {
		return ModelEntry{}, fmt.Errorf("id_registry: modell anlegen fehlgeschlagen: %w", err)
	}
	return ModelEntry{Provider: provider, UpstreamID: upstreamID, Shortcode: shortcode, AssignedAt: now}, nil
}

func (r *IDRegistry) shortcodeTakenLocked(shortcode string) (bool, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM models WHERE shortcode = ?`, shortcode).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("id_registry: shortcode-check fehlgeschlagen: %w", err)
	}
	return count > 0, nil
}
```

- [ ] **Step 4: Tests ausführen, Erfolg bestätigen**

Run: `go test ./sigoengine/... -run TestAssignModel -v`
Expected: PASS (alle vier Tests)

- [ ] **Step 5: Commit**

```bash
git add sigoengine/id_registry.go sigoengine/id_registry_test.go
git commit -m "feat: Modell-Shortcode assign-once mit Reaktivierung retired Modelle"
```

---

### Task 4: Sync + Retire-Logik (`SyncProvider`)

**Files:**
- Modify: `sigoengine/id_registry.go`
- Modify: `sigoengine/id_registry_test.go`

**Interfaces:**
- Consumes: `func (r *IDRegistry) AssignModel(provider, upstreamID string) (ModelEntry, error)` (Task 3)
- Produces: `func (r *IDRegistry) SyncProvider(provider string, seenUpstreamIDs []string) (map[string]ModelEntry, error)` — wird nur bei **erfolgreichem** Fetch aufgerufen; bei Fetch-Fehler einfach nicht aufrufen (das allein erfüllt "miss_streak bei Fehler unverändert").

- [ ] **Step 1: Failing Tests schreiben**

```go
func TestSyncProvider_PresentModelKeepsMissStreakZero(t *testing.T) {
	r := newTestIDRegistry(t)

	entries, err := r.SyncProvider("zai", []string{"glm-4.5"})
	if err != nil {
		t.Fatalf("SyncProvider: %v", err)
	}
	if _, ok := entries["glm-4.5"]; !ok {
		t.Fatal("glm-4.5 fehlt im Sync-Ergebnis")
	}

	entry, err := r.getModelLocked("zai", "glm-4.5")
	if err != nil || entry == nil {
		t.Fatalf("getModelLocked: %v, entry=%v", err, entry)
	}
	if entry.MissStreak != 0 {
		t.Errorf("miss_streak = %d, erwartet 0", entry.MissStreak)
	}
}

func TestSyncProvider_MissingModelIncrementsStreakWithoutRetiring(t *testing.T) {
	r := newTestIDRegistry(t)

	if _, err := r.SyncProvider("zai", []string{"glm-4.5"}); err != nil {
		t.Fatalf("SyncProvider (1): %v", err)
	}
	// Zweiter und dritter Sync ohne glm-4.5 in der Live-Liste.
	if _, err := r.SyncProvider("zai", []string{}); err != nil {
		t.Fatalf("SyncProvider (2): %v", err)
	}

	entry, err := r.getModelLocked("zai", "glm-4.5")
	if err != nil || entry == nil {
		t.Fatalf("getModelLocked: %v, entry=%v", err, entry)
	}
	if entry.MissStreak != 1 {
		t.Errorf("miss_streak = %d, erwartet 1", entry.MissStreak)
	}
	if entry.RetiredAt != nil {
		t.Error("Modell darf nach nur einem Miss noch nicht retired sein")
	}
}

func TestSyncProvider_RetiresAfterThreeConsecutiveMisses(t *testing.T) {
	r := newTestIDRegistry(t)

	if _, err := r.SyncProvider("zai", []string{"glm-4.5"}); err != nil {
		t.Fatalf("SyncProvider (initial): %v", err)
	}
	for i := 0; i < retireThreshold; i++ {
		if _, err := r.SyncProvider("zai", []string{}); err != nil {
			t.Fatalf("SyncProvider (miss %d): %v", i, err)
		}
	}

	entry, err := r.getModelLocked("zai", "glm-4.5")
	if err != nil || entry == nil {
		t.Fatalf("getModelLocked: %v, entry=%v", err, entry)
	}
	if entry.RetiredAt == nil {
		t.Fatal("Modell sollte nach 3 aufeinanderfolgenden Misses retired sein")
	}
}

func TestSyncProvider_ReappearingModelResetsStreakViaAssignModel(t *testing.T) {
	r := newTestIDRegistry(t)

	if _, err := r.SyncProvider("zai", []string{"glm-4.5"}); err != nil {
		t.Fatalf("SyncProvider (initial): %v", err)
	}
	if _, err := r.SyncProvider("zai", []string{}); err != nil {
		t.Fatalf("SyncProvider (miss): %v", err)
	}
	if _, err := r.SyncProvider("zai", []string{"glm-4.5"}); err != nil {
		t.Fatalf("SyncProvider (reappear): %v", err)
	}

	entry, err := r.getModelLocked("zai", "glm-4.5")
	if err != nil || entry == nil {
		t.Fatalf("getModelLocked: %v, entry=%v", err, entry)
	}
	if entry.MissStreak != 0 {
		t.Errorf("miss_streak = %d, erwartet 0 nach Wiederauftauchen", entry.MissStreak)
	}
}

func TestSyncProvider_DoesNotTouchOtherProviders(t *testing.T) {
	r := newTestIDRegistry(t)

	if _, err := r.SyncProvider("zai", []string{"glm-4.5"}); err != nil {
		t.Fatalf("SyncProvider(zai): %v", err)
	}
	if _, err := r.SyncProvider("longcat", []string{"longcat-flash"}); err != nil {
		t.Fatalf("SyncProvider(longcat): %v", err)
	}
	// Zweiter zai-Sync ohne glm-4.5 darf longcat-flash nicht anfassen.
	if _, err := r.SyncProvider("zai", []string{}); err != nil {
		t.Fatalf("SyncProvider(zai, leer): %v", err)
	}

	longcatEntry, err := r.getModelLocked("longcat", "longcat-flash")
	if err != nil || longcatEntry == nil {
		t.Fatalf("getModelLocked(longcat): %v, entry=%v", err, longcatEntry)
	}
	if longcatEntry.MissStreak != 0 {
		t.Errorf("longcat-flash miss_streak = %d, erwartet 0 (unberührt)", longcatEntry.MissStreak)
	}
}
```

- [ ] **Step 2: Tests ausführen, Fehlschlag bestätigen**

Run: `go test ./sigoengine/... -run TestSyncProvider -v`
Expected: FAIL — `SyncProvider` existiert nicht.

- [ ] **Step 3: `SyncProvider` implementieren**

In `sigoengine/id_registry.go` ergänzen:

```go
// SyncProvider gleicht die Live-Modell-Liste eines Providers mit der
// Registry ab. Nur bei einem ERFOLGREICHEN Fetch aufrufen — bei einem
// fehlgeschlagenen Fetch (Netzwerkfehler) diese Funktion einfach nicht
// aufrufen, damit miss_streak unverändert bleibt (schützt vor der
// dokumentierten ZAI/Longcat-Fallback-Asymmetrie).
func (r *IDRegistry) SyncProvider(provider string, seenUpstreamIDs []string) (map[string]ModelEntry, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("id_registry: registry nicht geöffnet")
	}

	result := make(map[string]ModelEntry, len(seenUpstreamIDs))
	seen := make(map[string]bool, len(seenUpstreamIDs))
	for _, id := range seenUpstreamIDs {
		seen[id] = true
		entry, err := r.AssignModel(provider, id)
		if err != nil {
			return nil, err
		}
		result[id] = entry
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	rows, err := r.db.Query(
		`SELECT upstream_id, miss_streak, retired_at FROM models WHERE provider = ?`,
		provider,
	)
	if err != nil {
		return nil, fmt.Errorf("id_registry: sync-scan fehlgeschlagen: %w", err)
	}
	type missUpdate struct {
		id      string
		streak  int
		retired bool
	}
	var updates []missUpdate
	for rows.Next() {
		var id string
		var missStreak int
		var retiredAt sql.NullInt64
		if err := rows.Scan(&id, &missStreak, &retiredAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("id_registry: sync-scan-zeile fehlgeschlagen: %w", err)
		}
		if seen[id] || retiredAt.Valid {
			continue
		}
		newStreak := missStreak + 1
		updates = append(updates, missUpdate{id: id, streak: newStreak, retired: newStreak >= retireThreshold})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("id_registry: sync-scan-iteration fehlgeschlagen: %w", err)
	}

	now := time.Now().Unix()
	for _, u := range updates {
		if u.retired {
			if _, err := r.db.Exec(
				`UPDATE models SET retired_at = ?, miss_streak = ? WHERE provider = ? AND upstream_id = ?`,
				now, u.streak, provider, u.id,
			); err != nil {
				return nil, fmt.Errorf("id_registry: retire-update fehlgeschlagen: %w", err)
			}
		} else {
			if _, err := r.db.Exec(
				`UPDATE models SET miss_streak = ? WHERE provider = ? AND upstream_id = ?`,
				u.streak, provider, u.id,
			); err != nil {
				return nil, fmt.Errorf("id_registry: miss-streak-update fehlgeschlagen: %w", err)
			}
		}
	}
	return result, nil
}
```

- [ ] **Step 4: Tests ausführen, Erfolg bestätigen**

Run: `go test ./sigoengine/... -run TestSyncProvider -v`
Expected: PASS (alle fünf Tests)

- [ ] **Step 5: Commit**

```bash
git add sigoengine/id_registry.go sigoengine/id_registry_test.go
git commit -m "feat: SyncProvider mit Miss-Streak-Retire-Logik (Debounce gegen Fetch-Fehler)"
```

---

### Task 5: Shortcode-Auflösung inkl. Kanal-Suffix (`ResolveShortcode`)

**Files:**
- Modify: `sigoengine/id_registry.go`
- Modify: `sigoengine/id_registry_test.go`

**Interfaces:**
- Produces: `func (r *IDRegistry) ResolveShortcode(input string) (ModelEntry, string, error)` — Rückgabe `(entry, channelSuffix, error)`; `channelSuffix == ""` bedeutet Default-Kanal. Bei Nichtfund: `ErrShortcodeNotFound`.

- [ ] **Step 1: Failing Tests schreiben**

```go
func TestResolveShortcode_ExactMatchNoChannel(t *testing.T) {
	r := newTestIDRegistry(t)
	assigned, err := r.AssignModel("zai", "glm-4.5")
	if err != nil {
		t.Fatalf("AssignModel: %v", err)
	}

	entry, channel, err := r.ResolveShortcode(assigned.Shortcode)
	if err != nil {
		t.Fatalf("ResolveShortcode: %v", err)
	}
	if channel != "" {
		t.Errorf("channel = %q, erwartet leer (Default)", channel)
	}
	if entry.UpstreamID != "glm-4.5" {
		t.Errorf("UpstreamID = %q, erwartet glm-4.5", entry.UpstreamID)
	}
}

func TestResolveShortcode_ChannelSuffixParsed(t *testing.T) {
	r := newTestIDRegistry(t)
	assigned, err := r.AssignModel("zai", "glm-4.5")
	if err != nil {
		t.Fatalf("AssignModel: %v", err)
	}

	entry, channel, err := r.ResolveShortcode(assigned.Shortcode + "-2")
	if err != nil {
		t.Fatalf("ResolveShortcode: %v", err)
	}
	if channel != "2" {
		t.Errorf("channel = %q, erwartet '2'", channel)
	}
	if entry.UpstreamID != "glm-4.5" {
		t.Errorf("UpstreamID = %q, erwartet glm-4.5", entry.UpstreamID)
	}
}

func TestResolveShortcode_CaseInsensitive(t *testing.T) {
	r := newTestIDRegistry(t)
	assigned, err := r.AssignModel("zai", "glm-4.5")
	if err != nil {
		t.Fatalf("AssignModel: %v", err)
	}

	_, _, err = r.ResolveShortcode(strings.ToUpper(assigned.Shortcode))
	if err != nil {
		t.Fatalf("ResolveShortcode (uppercase): %v", err)
	}
}

func TestResolveShortcode_NotFound(t *testing.T) {
	r := newTestIDRegistry(t)

	_, _, err := r.ResolveShortcode("does-not-exist")
	if !errors.Is(err, ErrShortcodeNotFound) {
		t.Errorf("err = %v, erwartet ErrShortcodeNotFound", err)
	}
}

func TestResolveShortcode_RetiredEntryStillResolves(t *testing.T) {
	r := newTestIDRegistry(t)
	assigned, err := r.AssignModel("zai", "glm-4.5")
	if err != nil {
		t.Fatalf("AssignModel: %v", err)
	}
	if _, err := r.db.Exec(
		`UPDATE models SET retired_at = ? WHERE provider = ? AND upstream_id = ?`,
		time.Now().Unix(), "zai", "glm-4.5",
	); err != nil {
		t.Fatalf("retired-Setup fehlgeschlagen: %v", err)
	}

	entry, _, err := r.ResolveShortcode(assigned.Shortcode)
	if err != nil {
		t.Fatalf("ResolveShortcode: %v", err)
	}
	if entry.RetiredAt == nil {
		t.Error("erwarte RetiredAt gesetzt — Aufrufer entscheidet über Fehlerbehandlung")
	}
}
```

- [ ] **Step 2: Tests ausführen, Fehlschlag bestätigen**

Run: `go test ./sigoengine/... -run TestResolveShortcode -v`
Expected: FAIL — `ResolveShortcode` existiert nicht.

- [ ] **Step 3: `ResolveShortcode` implementieren**

In `sigoengine/id_registry.go` ergänzen (Import `"errors"` ist bereits vorhanden für `ErrShortcodeNotFound`):

```go
// ResolveShortcode löst einen Shortcode auf, optional mit angehängtem
// Kanal-Suffix ("<shortcode>-<kanal>", z.B. "zai-glm45-2"). Reihenfolge
// wichtig: zuerst exakter Treffer (Default-Kanal), erst danach der
// Versuch, das letzte "-"-Segment als rein numerischen Kanal abzutrennen
// — Varianten-Suffixe aus GenerateShortcode sind nie rein numerisch
// (Buchstaben-Abkürzungen bzw. Cutter-Code mit führendem Buchstaben),
// daher ist die Trennung eindeutig.
func (r *IDRegistry) ResolveShortcode(input string) (ModelEntry, string, error) {
	if r == nil || r.db == nil {
		return ModelEntry{}, "", ErrShortcodeNotFound
	}
	lower := strings.ToLower(input)

	if entry, err := r.getByShortcodeLocked(lower); err == nil {
		return entry, "", nil
	} else if !errors.Is(err, ErrShortcodeNotFound) {
		return ModelEntry{}, "", err
	}

	idx := strings.LastIndex(lower, "-")
	if idx < 0 {
		return ModelEntry{}, "", ErrShortcodeNotFound
	}
	base, channel := lower[:idx], lower[idx+1:]
	if !isChannelSuffix(channel) {
		return ModelEntry{}, "", ErrShortcodeNotFound
	}
	entry, err := r.getByShortcodeLocked(base)
	if err != nil {
		return ModelEntry{}, "", err
	}
	return entry, channel, nil
}

func (r *IDRegistry) getByShortcodeLocked(shortcode string) (ModelEntry, error) {
	row := r.db.QueryRow(
		`SELECT provider, upstream_id, assigned_at, retired_at, miss_streak FROM models WHERE shortcode = ?`,
		shortcode,
	)
	var provider, upstreamID string
	var assignedAt int64
	var retiredAt sql.NullInt64
	var missStreak int
	err := row.Scan(&provider, &upstreamID, &assignedAt, &retiredAt, &missStreak)
	if err == sql.ErrNoRows {
		return ModelEntry{}, ErrShortcodeNotFound
	}
	if err != nil {
		return ModelEntry{}, fmt.Errorf("id_registry: shortcode lesen fehlgeschlagen: %w", err)
	}
	entry := ModelEntry{Provider: provider, UpstreamID: upstreamID, Shortcode: shortcode, AssignedAt: time.Unix(assignedAt, 0), MissStreak: missStreak}
	if retiredAt.Valid {
		t := time.Unix(retiredAt.Int64, 0)
		entry.RetiredAt = &t
	}
	return entry, nil
}

// isChannelSuffix prüft, ob s ausschließlich aus Ziffern besteht (Kanal-
// Nummer). Bewusst eigenständig statt der vorhandenen isDigitOnly aus
// shortcode.go, die zusätzlich '.' erlaubt (für Versionsnummern gedacht,
// hier fehl am Platz).
func isChannelSuffix(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Tests ausführen, Erfolg bestätigen**

Run: `go test ./sigoengine/... -run TestResolveShortcode -v`
Expected: PASS (alle fünf Tests)

- [ ] **Step 5: Gesamtes `sigoengine`-Paket testen**

Run: `go test ./sigoengine/... -v`
Expected: PASS, keine Regressionen in bestehenden Tests (`shortcode_test.go`, `costdb_test.go`, `channel_test.go`, etc.)

- [ ] **Step 6: Commit**

```bash
git add sigoengine/id_registry.go sigoengine/id_registry_test.go
git commit -m "feat: ResolveShortcode mit Kanal-Suffix-Parsing"
```

---

### Task 6: Server-Boot verdrahten (`loadModelsFromProviders` + `main()`)

**Files:**
- Modify: `sigoREST/main.go`

**Interfaces:**
- Consumes: `sigoengine.OpenIDRegistry`, `sigoengine.IDRegistry.SyncProvider`, `sigoengine.ModelEntry` (Tasks 1–4)
- Produces: `Server.idRegistry *sigoengine.IDRegistry` (neues Feld, für Task 7 gebraucht)
- Produces: `loadModelsFromProviders(reg *sigoengine.IDRegistry) map[string]ModelInfo` (Signaturänderung — einziger Aufrufer ist `main()`, kein Test referenziert die Funktion, siehe Vorab-Check unten)

Kein Unit-Test für diesen Task: `loadModelsFromProviders()` macht echte HTTP-Calls gegen Live-Provider-APIs und hat laut CLAUDE.md bewusst keine Go-Tests ("Server + CLI selbst haben keine Go-Tests → manuell via CLI/REST-API testen"). Verifikation über Build + manuellen Boot-Test (Steps 4–5).

- [ ] **Step 1: `Server`-Struct um `idRegistry` erweitern**

In `sigoREST/main.go`, im `Server`-Struct (aktuell Zeile 152–167):

Ersetze:

```go
	baseDir         string
	costDB          *sigoengine.CostDB // persistentes Kosten-Tracking (costs.db im data-dir); nil-safe
}
```

durch:

```go
	baseDir         string
	costDB          *sigoengine.CostDB      // persistentes Kosten-Tracking (costs.db im data-dir); nil-safe
	idRegistry      *sigoengine.IDRegistry  // persistente Shortcode-/Provider-Kürzel-Registry (id_registry.db im data-dir); nil-safe
}
```

- [ ] **Step 2: `loadModelsFromProviders` auf Registry-Sync umstellen**

Ersetze die komplette Funktion (aktuell Zeile 355–413):

```go
// loadModelsFromProviders ruft alle Provider-APIs beim Start ab.
// Fehler bei einzelnen Providern werden geloggt; der Server startet
// trotzdem mit den verfügbaren Modellen.
func loadModelsFromProviders() map[string]ModelInfo {
	models := make(map[string]ModelInfo)

	// Retry-Parameter: 4 Versuche mit 2s/4s/8s Backoff. Fängt den Fall ab,
	// dass beim Systemstart DNS noch nicht verfügbar ist (siehe FetchWithRetry).
	const fetchAttempts = 4
	const fetchBackoff = 2 * time.Second

	// 1. Mammouth (kein API-Key nötig)
	if ms, err := sigoengine.FetchWithRetry("mammouth", fetchAttempts, fetchBackoff, sigoengine.FetchMammouthModels); err != nil {
		sigoengine.LogWarn("Mammouth-Modelle nicht geladen", map[string]interface{}{"error": err.Error()})
	} else {
		for _, m := range ms {
			models[m.ID] = modelInfoFromEngine(m)
		}
	}

	// 2. Moonshot
	if ms, err := sigoengine.FetchWithRetry("moonshot", fetchAttempts, fetchBackoff, sigoengine.FetchMoonshotModels); err != nil {
		sigoengine.LogWarn("Moonshot-Modelle nicht geladen", map[string]interface{}{"error": err.Error()})
	} else {
		for _, m := range ms {
			models[m.ID] = modelInfoFromEngine(m)
		}
	}

	// 3. ZAI (fällt intern auf statische Liste zurück)
	if ms, err := sigoengine.FetchWithRetry("zai", fetchAttempts, fetchBackoff, sigoengine.FetchZAIModels); err != nil {
		sigoengine.LogWarn("ZAI-Modelle nicht geladen", map[string]interface{}{"error": err.Error()})
	} else {
		for _, m := range ms {
			models[m.ID] = modelInfoFromEngine(m)
		}
	}

	// 4. Longcat (fällt intern auf statische Liste zurück)
	if ms, err := sigoengine.FetchWithRetry("longcat", fetchAttempts, fetchBackoff, sigoengine.FetchLongcatModels); err != nil {
		sigoengine.LogWarn("Longcat-Modelle nicht geladen", map[string]interface{}{"error": err.Error()})
	} else {
		for _, m := range ms {
			models[m.ID] = modelInfoFromEngine(m)
		}
	}

	// 5. Cheaperinference (Aggregator; IDs mit "ci-" präfixt, siehe UpstreamID)
	if ms, err := sigoengine.FetchWithRetry("cheaperinference", fetchAttempts, fetchBackoff, sigoengine.FetchCheaperinferenceModels); err != nil {
		sigoengine.LogWarn("Cheaperinference-Modelle nicht geladen", map[string]interface{}{"error": err.Error()})
	} else {
		for _, m := range ms {
			models[m.ID] = modelInfoFromEngine(m)
		}
	}

	sigoengine.LogInfo("Provider-Modelle geladen", map[string]interface{}{"count": len(models)})
	return models
}
```

durch:

```go
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
			ids := make([]string, len(ms))
			for i, m := range ms {
				ids[i] = m.ID
			}
			entries, err = reg.SyncProvider(f.provider, ids)
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
```

- [ ] **Step 3: `main()` um Registry-Init erweitern**

In `sigoREST/main.go`, `func main()`: ersetze

```go
	// Server-State initialisieren
	srv := &Server{
		models:         loadModelsFromProviders(),
		memory:         loadMemory(*dataDir),
		breakers:       make(map[string]*sigoengine.EnhancedCircuitBreaker),
		systemPrompt:   loadSystemPrompt(*dataDir),
		usage:          make(map[string]*ModelUsageStats),
		usageByChannel: make(map[string]*ModelUsageStats),
		baseDir:        *dataDir,
	}
```

durch:

```go
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
```

Hinweis: `main()` deklariert weiter unten bereits `costDB, err := sigoengine.OpenCostDB(...)` — da `err` jetzt schon oben deklariert wurde, muss diese Zeile von `:=` unverändert bleiben (neue Variable `costDB` im selben Scope, `err` wird per Go-Regel wiederverwendet, solange mindestens eine neue Variable auf der linken Seite steht — das ist hier der Fall). Kein weiterer Eingriff nötig.

- [ ] **Step 4: Build verifizieren**

Run: `go build ./...`
Expected: kompiliert ohne Fehler.

- [ ] **Step 5: Manueller Boot-Test**

```bash
make sigorest
./build/sigoREST -v debug -data-dir /tmp/sigorest-test-data
```

Erwartung im Log: `"ID-Registry aktiv"` mit Pfad `/tmp/sigorest-test-data/id_registry.db`, danach `"Provider-Modelle geladen"`. Server mit Ctrl+C stoppen, Datei prüfen:

```bash
ls -la /tmp/sigorest-test-data/id_registry.db
```

Erwartung: Datei existiert. Server ein zweites Mal starten, `/api/shortcodes` abfragen, mit dem ersten Lauf vergleichen (gleiche Modelle → gleiche Shortcodes):

```bash
curl -s http://localhost:9080/api/shortcodes | head -20
```

- [ ] **Step 6: Commit**

```bash
git add sigoREST/main.go
git commit -m "feat: sigoREST-Boot mit persistenter ID-Registry verdrahten"
```

---

### Task 7: Retired-Fehlerpfad + Kanal-Override in `lookupModel`

**Files:**
- Modify: `sigoREST/main.go`
- Modify: `sigoREST/anthropic.go`
- Modify: `sigoREST/main_test.go`

**Interfaces:**
- Consumes: `sigoengine.IDRegistry.ResolveShortcode`, `sigoengine.ErrShortcodeNotFound` (Task 5), `Server.idRegistry` (Task 6)
- Produces: `type lookupResult struct { Info ModelInfo; ID string; Channel string; Retired bool; RetiredAt time.Time }`
- Produces: `func (s *Server) lookupModel(query string) (lookupResult, bool)` (Signaturänderung — ersetzt bisheriges `(ModelInfo, string, bool)`; Aufrufer: `handleChatCompletions` in `main.go`, `handleMessages` in `anthropic.go`)

- [ ] **Step 1: Failing Test schreiben**

```go
// sigoREST/main_test.go, am Ende der Datei ergänzen
func TestLookupModel_ChannelSuffixAndRetired(t *testing.T) {
	srv, dir := newTestServer(t)

	reg, err := sigoengine.OpenIDRegistry(dir)
	if err != nil {
		t.Fatalf("OpenIDRegistry: %v", err)
	}
	t.Cleanup(func() { reg.Close() })
	srv.idRegistry = reg

	active, err := reg.AssignModel("zai", "glm-4.5")
	if err != nil {
		t.Fatalf("AssignModel(active): %v", err)
	}
	srv.models["glm-4.5"] = ModelInfo{ID: "glm-4.5", Shortcode: active.Shortcode}

	retired, err := reg.AssignModel("zai", "glm-4.4-old")
	if err != nil {
		t.Fatalf("AssignModel(retired): %v", err)
	}
	if _, err := reg.SyncProvider("zai", []string{"glm-4.5"}); err != nil {
		t.Fatalf("SyncProvider: %v", err)
	}
	for i := 0; i < 2; i++ { // insgesamt 3 Misses inkl. des Sync oben
		if _, err := reg.SyncProvider("zai", []string{"glm-4.5"}); err != nil {
			t.Fatalf("SyncProvider (miss %d): %v", i, err)
		}
	}

	t.Run("exact ID match", func(t *testing.T) {
		lr, ok := srv.lookupModel("glm-4.5")
		if !ok || lr.ID != "glm-4.5" || lr.Retired {
			t.Fatalf("lookupModel(glm-4.5) = %+v, ok=%v", lr, ok)
		}
	})

	t.Run("channel suffix on shortcode", func(t *testing.T) {
		lr, ok := srv.lookupModel(active.Shortcode + "-2")
		if !ok {
			t.Fatal("lookupModel mit Kanal-Suffix nicht gefunden")
		}
		if lr.ID != "glm-4.5" {
			t.Errorf("ID = %q, erwartet glm-4.5", lr.ID)
		}
		if lr.Channel != "2" {
			t.Errorf("Channel = %q, erwartet '2'", lr.Channel)
		}
	})

	t.Run("retired model resolves with Retired flag", func(t *testing.T) {
		lr, ok := srv.lookupModel(retired.Shortcode)
		if !ok {
			t.Fatal("lookupModel für retired Shortcode nicht gefunden")
		}
		if !lr.Retired {
			t.Error("erwarte Retired == true")
		}
		if lr.RetiredAt.IsZero() {
			t.Error("erwarte gesetztes RetiredAt")
		}
	})

	t.Run("unknown shortcode", func(t *testing.T) {
		_, ok := srv.lookupModel("does-not-exist")
		if ok {
			t.Error("erwarte ok=false für unbekannten Shortcode")
		}
	})
}
```

- [ ] **Step 2: Test ausführen, Fehlschlag bestätigen**

Run: `go test ./sigoREST/... -run TestLookupModel_ChannelSuffixAndRetired -v`
Expected: FAIL — Compile-Fehler, da `lookupModel` noch `(ModelInfo, string, bool)` zurückgibt und `retired.Shortcode` etc. nicht existieren.

- [ ] **Step 3: `lookupModel` umbauen**

In `sigoREST/main.go`, ersetze (aktuell Zeile 500–518):

```go
// lookupModel sucht case-insensitiv nach ID (Map-Key) oder Shortcode.
// Aufrufer muss s.mu (RLock) halten. Liefert ModelInfo + kanonische ID.
// Provider-Modell-IDs sind überwiegend lowercase; Sigil/CLI können aber
// andere Casing mitschicken (z.B. "GLM-4.5"), die ansonsten am exakten
// Map-Key-Lookup scheitern würden.
func (s *Server) lookupModel(query string) (ModelInfo, string, bool) {
	q := strings.ToLower(query)
	for id, info := range s.models {
		if strings.ToLower(id) == q {
			return info, id, true
		}
	}
	for _, info := range s.models {
		if strings.ToLower(info.Shortcode) == q {
			return info, info.ID, true
		}
	}
	return ModelInfo{}, "", false
}
```

durch:

```go
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

// lookupModel sucht case-insensitiv nach ID (Map-Key) oder Shortcode.
// Aufrufer muss s.mu (RLock) halten. Provider-Modell-IDs sind
// überwiegend lowercase; Sigil/CLI können aber andere Casing mitschicken
// (z.B. "GLM-4.5"), die ansonsten am exakten Map-Key-Lookup scheitern
// würden.
//
// Reihenfolge: (1) exakte ID, (2) exakter Shortcode aus s.models — beide
// decken den aktiven Default-Kanal-Fall ohne Registry-Zugriff ab. Erst
// danach (3) die persistente ID-Registry, die zusätzlich Shortcodes mit
// Kanal-Suffix ("zai-glm45-2") und retired Modelle auflöst, welche per
// Definition nicht mehr in s.models stehen.
func (s *Server) lookupModel(query string) (lookupResult, bool) {
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

	if s.idRegistry == nil {
		return lookupResult{}, false
	}
	entry, channel, err := s.idRegistry.ResolveShortcode(q)
	if err != nil {
		return lookupResult{}, false
	}
	res := lookupResult{ID: entry.UpstreamID, Channel: channel}
	if entry.RetiredAt != nil {
		res.Retired = true
		res.RetiredAt = *entry.RetiredAt
		return res, true
	}
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
```

- [ ] **Step 4: Aufrufer in `handleChatCompletions` anpassen**

In `sigoREST/main.go`, `handleChatCompletions` (aktuell Zeile 632–648), ersetze:

```go
	// Modell-Validierung (ID oder Shortcode, case-insensitiv)
	modelID := req.Model

	// Case-insensitiv nach ID oder Shortcode suchen
	s.mu.RLock()
	modelInfo, resolvedID, exists := s.lookupModel(modelID)
	if exists {
		modelID = resolvedID
	}
	if !exists {
		s.mu.RUnlock()
		writeError(w, fmt.Sprintf("Model '%s' nicht gefunden", req.Model), "model_not_found", http.StatusBadRequest)
		return
	}
	mem := s.memory
	globalSystemPrompt := s.systemPrompt
	s.mu.RUnlock()
```

durch:

```go
	// Modell-Validierung (ID oder Shortcode, case-insensitiv)
	modelID := req.Model

	// Case-insensitiv nach ID oder Shortcode suchen
	s.mu.RLock()
	lr, exists := s.lookupModel(modelID)
	if exists {
		modelID = lr.ID
	}
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
```

- [ ] **Step 5: Aufrufer in `handleMessages` (Anthropic-Bridge) anpassen**

In `sigoREST/anthropic.go`, ersetze (aktuell Zeile 409–415):

```go
	s.mu.RLock()
	modelInfo, modelID, exists := s.lookupModel(req.Model)
	s.mu.RUnlock()
	if !exists {
		writeAnthropicError(w, "not_found_error", fmt.Sprintf("Model '%s' nicht gefunden", req.Model), http.StatusNotFound)
		return
	}
```

durch:

```go
	s.mu.RLock()
	lr, exists := s.lookupModel(req.Model)
	s.mu.RUnlock()
	if !exists {
		writeAnthropicError(w, "not_found_error", fmt.Sprintf("Model '%s' nicht gefunden", req.Model), http.StatusNotFound)
		return
	}
	if lr.Retired {
		writeAnthropicError(w, "not_found_error", fmt.Sprintf(
			"Modell '%s' ist seit %s nicht mehr verfügbar. Kein automatischer Fallback auf ein anderes Modell.",
			req.Model, lr.RetiredAt.Format("2006-01-02"),
		), http.StatusGone)
		return
	}
	modelInfo, modelID := lr.Info, lr.ID
	if lr.Channel != "" && req.Channel == "" {
		req.Channel = lr.Channel
	}
```

`AnthropicRequest` hat kein Channel-Feld im Wire-Format und soll auch keins bekommen (Anthropic-Clients kennen sigoREST-Kanäle nicht) — der Kanal-Override aus dem Shortcode-Suffix wird stattdessen als lokale Variable durchgereicht. Ein paar Zeilen weiter unten in `handleMessages`, ersetze:

```go
	provider := s.providerForModel(modelID)
	ch, err := s.channelManager.Resolve(provider, "")
```

durch:

```go
	provider := s.providerForModel(modelID)
	ch, err := s.channelManager.Resolve(provider, lr.Channel)
```

`ChannelManager.Resolve` behandelt einen leeren String bereits als "kein Override, Default-Auswahl" (siehe bestehendes Verhalten bei `handleChatCompletions`, das genauso `req.Channel` durchreicht) — `lr.Channel == ""` (Default-Kanal-Fall) verhält sich also identisch zum bisherigen hartcodierten `""`.

- [ ] **Step 6: Tests ausführen, Erfolg bestätigen**

Run: `go test ./sigoREST/... -run TestLookupModel_ChannelSuffixAndRetired -v`
Expected: PASS

- [ ] **Step 7: Gesamtes Repository testen**

Run: `go build ./... && go test ./...`
Expected: PASS, keine Regressionen (insbesondere `TestHandleChatCompletions_MidStreamFailureDoesNotDoubleWriteOrGlueJSON` und die Anthropic-Bridge-Tests, die `lookupModel` indirekt über die Handler durchlaufen).

- [ ] **Step 8: Manueller End-to-End-Test**

```bash
make build
./build/sigoREST -v debug -data-dir /tmp/sigorest-test-data &
sleep 2
curl -s http://localhost:9080/api/shortcodes | jq
# Einen der zurückgegebenen Shortcodes für einen echten Chat-Call verwenden:
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"<shortcode-aus-obiger-liste>","messages":[{"role":"user","content":"Hallo"}]}'
kill %1
```

Erwartung: normaler Chat-Response. Ein erfundener, garantiert unbekannter Shortcode liefert `model_not_found` (HTTP 400), kein Absturz.

- [ ] **Step 9: Commit**

```bash
git add sigoREST/main.go sigoREST/anthropic.go sigoREST/main_test.go
git commit -m "feat: retired Modelle mit HTTP 410 ablehnen, Kanal-Suffix in Shortcodes auflösen"
```

---

### Task 8: TODO.md-Spezifikationsfehler korrigieren

**Files:**
- Modify: `TODO.md`

Reiner Doku-Task, kein Code. Behebt die beiden in der Spec (Abschnitt "Full-ID-Format — Korrekturen") benannten Fehler im ursprünglichen Text und ersetzt den verworfenen Positions-Shortcode-Vorschlag durch einen Verweis auf die neue Spec.

- [ ] **Step 1: Zeichensatz- und Padding-Fehler korrigieren, Shortcode-Abschnitt ersetzen**

Ersetze in `TODO.md` den kompletten Abschnitt ab `## ID Vereinfachung` bis zum Ende von `## Shortcode` (Zeilen 6–37) durch:

```markdown
## ID Vereinfachung

lass uns die ID komplett ändern. In zukunft soll das Format der api-id sein:
 "provider:model:channe" ==>  "omnir:cl-haiku45:0:"
- provider => 5stelliger Providercode,
  wichtig: zai wird zu zai00 oder zai__  beides erlaubt (5stellig)
- model => 10stelliger Modellcode, wie sonnet5 -> sonnet5___
- channel => 0==default und >0 die Nummer des channels

Alle ID-Codes bestehen aus Großbuchstaben und Zahlen und : und _ ;
im Modell-Feld zusätzlich `-` erlaubt (Upstream-Modellnamen wie
"gpt-4o"/"glm-4.5" enthalten routinemäßig Bindestriche).

Beispiel
```python
st="omnir:cl-haiku45:0:"
st=st.upper()
prv = st[:5]    # omnir
mod = st[6:16]  # cl-haiku45
chn = st[17:18] # 0
```

---

## Shortcode

**Erledigt, siehe `docs/superpowers/specs/2026-09-20-id-shortcode-registry-design.md`
und `docs/superpowers/plans/2026-09-20-id-shortcode-registry.md`.**

Ursprünglicher Positions-Vorschlag (`p1m1c0`, Position in sortierter
Provider-/Modell-Liste) verworfen: sigoREST lädt Modelle dynamisch bei
jedem Boot, eine Positions-Nummer verschiebt sich bei jeder Änderung der
Live-Liste. Ersetzt durch eine persistente SQLite-Registry mit
assign-once Provider-/Modell-Kürzeln (Format `{provider3}-{semanticCode}
[-{channel}]`, z.B. `zai-glm45-2`) — siehe Spec für Details.
```

- [ ] **Step 2: Commit**

```bash
git add TODO.md
git commit -m "docs: TODO.md Spezifikationsfehler korrigieren, Shortcode-Abschnitt auf neue Spec verweisen"
```
