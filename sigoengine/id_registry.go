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

// ProviderModelSeed verbindet eine Live-Upstream-Modell-ID mit dem
// semantischen Shortcode-Bestandteil, den der Aufrufer für dieses Modell
// bereits berechnet hat (typischerweise Model.Shortcode aus einem
// Provider-Fetcher). Der Unterschied ist wesentlich: die Fetcher rufen
// GenerateShortcode mit einer über den gesamten Fetch mitlaufenden
// used-Map auf, ihre Codes sind daher innerhalb eines Providers
// tatsächlich verschieden. Eine blinde Neuberechnung pro ID (used == nil)
// ist das nicht — bei Providern ohne Familien-Präfix (cheaperinference,
// unbekannte Longcat-Modelle) fällt GenerateShortcode auf cutterCode über
// die ganze ID zurück und liefert für alle Modelle denselben Code.
// Leerer SemanticHint fällt auf genau diese Neuberechnung in AssignModel
// zurück.
type ProviderModelSeed struct {
	UpstreamID   string
	SemanticHint string
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

// **********************************************************************
// Provider-Kürzel-Verwaltung (Task 2+)

func (r *IDRegistry) providerCodeLocked(provider string) (string, error) {
	code, err := r.lookupProviderCodeLocked(provider)
	if err != nil {
		return "", err
	}
	if code != "" {
		return code, nil
	}

	// Kandidaten in Prioritätsreihenfolge: 3-Zeichen-Präfix, danach der
	// Cutter-Sanborn-Code, danach dessen letzte Stelle numerisch
	// durchprobiert (extrem unwahrscheinlicher Doppel-Kollisionsfall).
	cutter := normalizeCode(cutterCode(provider), 3)
	candidates := []string{normalizeCode(provider, 3), cutter}
	for i := 0; i < 10; i++ {
		candidates = append(candidates, fmt.Sprintf("%s%d", cutter[:2], i))
	}

	for _, candidate := range candidates {
		taken, err := r.providerCodeTakenLocked(candidate)
		if err != nil {
			return "", err
		}
		if taken {
			continue
		}
		_, err = r.db.Exec(
			`INSERT INTO providers (name, code, assigned_at) VALUES (?, ?, ?)`,
			provider, candidate, time.Now().Unix(),
		)
		if err == nil {
			return candidate, nil
		}
		if !isUniqueConstraintErr(err) {
			return "", fmt.Errorf("id_registry: provider anlegen fehlgeschlagen: %w", err)
		}
		// UNIQUE-Konflikt: zwischen SELECT und INSERT war ein zweiter
		// Prozess schneller (r.mu serialisiert nur in-process, nicht gegen
		// eine zweite sigoREST-Instanz auf derselben id_registry.db).
		// Entweder hat er denselben Provider angelegt — dann gilt sein Code
		// (assign-once bleibt gewahrt) — oder er hat sich nur diesen Code
		// gegriffen, dann weiter mit dem nächsten Kandidaten.
		code, lookupErr := r.lookupProviderCodeLocked(provider)
		if lookupErr != nil {
			return "", lookupErr
		}
		if code != "" {
			return code, nil
		}
	}
	return "", fmt.Errorf("id_registry: kein freier 3-Zeichen-Code für Provider %q gefunden", provider)
}

// lookupProviderCodeLocked liefert den gespeicherten Code oder "" wenn der
// Provider noch nicht in der Tabelle steht.
func (r *IDRegistry) lookupProviderCodeLocked(provider string) (string, error) {
	var code string
	err := r.db.QueryRow(`SELECT code FROM providers WHERE name = ?`, provider).Scan(&code)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("id_registry: provider-code lesen fehlgeschlagen: %w", err)
	}
	return code, nil
}

// isUniqueConstraintErr erkennt eine verletzte UNIQUE-/PRIMARY-KEY-
// Bedingung. Der Treiber modernc.org/sqlite reicht hier keinen
// typisierten Sentinel-Fehler durch (kein errors.Is-Ziel, nur ein
// generischer *sqlite.Error mit Textmeldung), daher bleibt als Test nur
// der String-Vergleich auf die von SQLite erzeugte Meldung
// "UNIQUE constraint failed: <tabelle>.<spalte>".
func isUniqueConstraintErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
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

// **********************************************************************
// Modell-Shortcode-Verwaltung (Task 3+)

// AssignModel liefert den Registry-Eintrag für (provider, upstreamID):
// existiert er aktiv -> unverändert zurückgeben; existiert er retired ->
// reaktivieren (gleicher Shortcode, miss_streak=0, retired_at=NULL);
// existiert er nicht -> neu anlegen mit einmalig vergebenem Shortcode.
//
// semanticHint ist der vom Aufrufer bereits berechnete semantische
// Shortcode-Bestandteil (siehe ProviderModelSeed); leer = selbst per
// GenerateShortcode berechnen. Der Hint wird NUR beim allerersten
// Anlegen eines Modells ausgewertet — bestehende und reaktivierte
// Einträge behalten ihren Shortcode unverändert (assign-once).
func (r *IDRegistry) AssignModel(provider, upstreamID, semanticHint string) (ModelEntry, error) {
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

	semantic := semanticHint
	if semantic == "" {
		semantic = GenerateShortcode(upstreamID, nil)
	}
	base := providerCode + "-" + semantic

	// Kollisionsauflösung: Suffix ".2", ".3", ... (bewusst "." und nicht
	// "-", siehe ResolveShortcode — "-" trennt den Kanal ab). Die Schleife
	// ist gedeckelt, damit ein unerwartet dauerhaft scheiternder INSERT
	// irgendwann einen Fehler liefert statt endlos zu laufen.
	const maxShortcodeAttempts = 1000
	shortcode := base
	for attempt := 0; attempt < maxShortcodeAttempts; attempt++ {
		if attempt > 0 {
			shortcode = fmt.Sprintf("%s.%d", base, attempt+1)
		}
		taken, err := r.shortcodeTakenLocked(shortcode)
		if err != nil {
			return ModelEntry{}, err
		}
		if taken {
			continue
		}

		now := time.Now()
		_, err = r.db.Exec(
			`INSERT INTO models (provider, upstream_id, shortcode, assigned_at, retired_at, miss_streak)
			 VALUES (?, ?, ?, ?, NULL, 0)`,
			provider, upstreamID, shortcode, now.Unix(),
		)
		if err == nil {
			return ModelEntry{Provider: provider, UpstreamID: upstreamID, Shortcode: shortcode, AssignedAt: now}, nil
		}
		if !isUniqueConstraintErr(err) {
			return ModelEntry{}, fmt.Errorf("id_registry: modell anlegen fehlgeschlagen: %w", err)
		}
		// UNIQUE-Konflikt zwischen Prüfung und INSERT (zweiter Prozess auf
		// derselben DB, r.mu serialisiert nur in-process). Zwei Fälle:
		// (a) er hat dasselbe Modell angelegt -> PRIMARY-KEY-Konflikt, sein
		// Eintrag gilt (assign-once); (b) er hat nur denselben Shortcode
		// belegt -> nächster Kandidat.
		existing, getErr := r.getModelLocked(provider, upstreamID)
		if getErr != nil {
			return ModelEntry{}, getErr
		}
		if existing != nil {
			return *existing, nil
		}
	}
	return ModelEntry{}, fmt.Errorf(
		"id_registry: kein freier Shortcode für %s/%s nach %d Versuchen (Basis %q)",
		provider, upstreamID, maxShortcodeAttempts, base,
	)
}

func (r *IDRegistry) shortcodeTakenLocked(shortcode string) (bool, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM models WHERE shortcode = ?`, shortcode).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("id_registry: shortcode-check fehlgeschlagen: %w", err)
	}
	return count > 0, nil
}

// **********************************************************************
// Sync + Retire-Logik (Task 4+)

// SyncProvider gleicht die Live-Modell-Liste eines Providers mit der
// Registry ab. Nur bei einem ERFOLGREICHEN Fetch aufrufen — bei einem
// fehlgeschlagenen Fetch (Netzwerkfehler) diese Funktion einfach nicht
// aufrufen, damit miss_streak unverändert bleibt (schützt vor der
// dokumentierten ZAI/Longcat-Fallback-Asymmetrie).
//
// Fehler-Semantik (bewusst asymmetrisch): scheitert AssignModel für ein
// EINZELNES Modell, wird das nur geloggt und das Modell übersprungen —
// es fehlt dann in der Ergebnis-Map und der Aufrufer fällt für genau
// dieses Modell auf seinen pro-Boot berechneten Shortcode zurück. Früher
// brach der erste Fehler den kompletten Provider-Sync ab, womit ALLE
// Modelle des Providers ihren stabilen Shortcode für diesen Boot
// verloren. Der error-Rückgabewert ist deshalb strukturellen Fehlern
// vorbehalten (Registry nicht geöffnet, Scan-/Update-Query gescheitert).
func (r *IDRegistry) SyncProvider(provider string, seeds []ProviderModelSeed) (map[string]ModelEntry, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("id_registry: registry nicht geöffnet")
	}

	result := make(map[string]ModelEntry, len(seeds))
	seen := make(map[string]bool, len(seeds))
	for _, seed := range seeds {
		// Auch ein fehlgeschlagenes Modell zählt als "gesehen": es steht
		// live beim Provider, sein miss_streak darf nicht hochlaufen.
		seen[seed.UpstreamID] = true
		entry, err := r.AssignModel(provider, seed.UpstreamID, seed.SemanticHint)
		if err != nil {
			LogWarn("ID-Registry: Modell-Zuweisung übersprungen", map[string]interface{}{
				"provider": provider, "upstream_id": seed.UpstreamID, "error": err.Error(),
			})
			continue
		}
		result[seed.UpstreamID] = entry
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Resette miss_streak zu 0 für alle sichtbaren Modelle (falls sie > 0 waren
	// von früheren Syncs). Das geschieht nach AssignModel, um sicherzustellen,
	// dass wiederaufgetauchte Modelle wieder auf 0 zurückgesetzt werden.
	for id := range seen {
		if _, err := r.db.Exec(
			`UPDATE models SET miss_streak = 0 WHERE provider = ? AND upstream_id = ?`,
			provider, id,
		); err != nil {
			return nil, fmt.Errorf("id_registry: miss-streak-reset fehlgeschlagen: %w", err)
		}
	}

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

// **********************************************************************
// Shortcode-Auflösung (Task 5)

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

	if entry, err := r.getByShortcode(lower); err == nil {
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
	entry, err := r.getByShortcode(base)
	if err != nil {
		return ModelEntry{}, "", err
	}
	return entry, channel, nil
}

func (r *IDRegistry) getByShortcode(shortcode string) (ModelEntry, error) {
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
