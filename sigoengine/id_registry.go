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
