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

// **********************************************************************
// Provider-Kürzel-Verwaltung (Task 2+)

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

// **********************************************************************
// Sync + Retire-Logik (Task 4+)

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
