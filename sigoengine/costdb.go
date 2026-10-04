//**********************************************************************
//      sigoengine/costdb.go
//**********************************************************************
//  Autor    : Gerhard Quell - gquell@skequell.de
//  CoAutor  : Claude (Hermes-Session, sigoREST-Kosten-Tracking)
//  Copyright: 2025 Gerhard Quell - SKEQuell
//**********************************************************************
//  Beschreibung: Persistentes Kosten-Tracking für sigoREST.
//
//  Bisher lebte der Token-Verbrauch nur in RAM (Server.usage /
//  Server.usageByChannel) und ging bei jedem Neustart verloren — keine
//  Historie, keine Budget-Kontrolle möglich. CostDB schreibt jedes
//  Request-Ergebnis als append-only Event in eine lokale SQLite-Datei
//  (<data-dir>/costs.db), rechnet Kosten in USD anhand der Modell-Preise
//  (ModelInfo.InputCost/OutputCost, $/1M Tokens) und liefert aggregierte
//  Auswertungen (heute/Monat/Zeitraum) sowie ein einfaches Budget-System
//  (Warnung + optionaler Hard-Stop).
//
//  Treiber: modernc.org/sqlite (pure Go, kein CGO — passt zum
//  bestehenden -trimpath-Build ohne C-Toolchain).
//**********************************************************************

package sigoengine

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// **********************************************************************
// Typen

// UsageEvent ist ein einzelner abgerechneter API-Call.
type UsageEvent struct {
	Timestamp     time.Time
	Model         string
	Provider      string
	Channel       string
	SessionID     string // optional, leer wenn keine Session verwendet wurde
	InputTokens   int64
	OutputTokens  int64
	TotalTokens   int64
	InputCostUSD  float64
	OutputCostUSD float64
	TotalCostUSD  float64
}

// CostSummary ist eine aggregierte Auswertung über einen Zeitraum.
type CostSummary struct {
	Since        time.Time            `json:"since"`
	Until        time.Time            `json:"until"`
	Requests     int64                `json:"requests"`
	InputTokens  int64                `json:"input_tokens"`
	OutputTokens int64                `json:"output_tokens"`
	TotalTokens  int64                `json:"total_tokens"`
	TotalCostUSD float64              `json:"total_cost_usd"`
	ByModel      map[string]*CostStat `json:"by_model"`
	ByChannel    map[string]*CostStat `json:"by_channel"`
}

// CostStat ist ein Teil-Aggregat (pro Modell oder pro Kanal).
type CostStat struct {
	Requests     int64   `json:"requests"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	TotalCostUSD float64 `json:"total_cost_usd"`
}

// BudgetConfig ist die persistierte Budget-Konfiguration (Singleton-Zeile).
type BudgetConfig struct {
	DailyLimitUSD   float64 `json:"daily_limit_usd"`   // 0 = kein Limit
	MonthlyLimitUSD float64 `json:"monthly_limit_usd"` // 0 = kein Limit
	HardStopEnabled bool    `json:"hard_stop_enabled"` // false = nur Warnung/Tracking, kein Request-Abbruch
}

// BudgetStatus ist das Ergebnis einer Budget-Prüfung vor einem Request.
type BudgetStatus struct {
	DailySpendUSD   float64 `json:"daily_spend_usd"`
	MonthlySpendUSD float64 `json:"monthly_spend_usd"`
	DailyLimitUSD   float64 `json:"daily_limit_usd"`
	MonthlyLimitUSD float64 `json:"monthly_limit_usd"`
	DailyExceeded   bool    `json:"daily_exceeded"`
	MonthlyExceeded bool    `json:"monthly_exceeded"`
	HardStopEnabled bool    `json:"hard_stop_enabled"`
	// Blocked ist true, wenn HardStopEnabled UND (DailyExceeded || MonthlyExceeded).
	// Aufrufer soll den Request in diesem Fall ablehnen (HTTP 402/429).
	Blocked bool `json:"blocked"`
}

// CostDB kapselt die SQLite-Verbindung. Ein Writer (RecordUsage), viele
// Reader (Summary/BudgetStatus) — WAL erlaubt das ohne gegenseitige
// Blockade.
type CostDB struct {
	db   *sql.DB
	path string
	mu   sync.Mutex // seriellisiert Writes zusätzlich zu WAL (SQLite mag keine parallelen Writer)
}

// **********************************************************************
// Öffnen / Schema

// OpenCostDB öffnet (oder erstellt) <dataDir>/costs.db, aktiviert WAL und
// legt das Schema an, falls nötig. dataDir wird angelegt, falls es fehlt.
func OpenCostDB(dataDir string) (*CostDB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("costdb: data-dir anlegen fehlgeschlagen: %w", err)
	}
	path := filepath.Join(dataDir, "costs.db")

	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("costdb: öffnen fehlgeschlagen: %w", err)
	}
	// Ein Event-Log mit gelegentlichen Bursts (Retry/Failover) braucht keinen
	// großen Connection-Pool; SQLite serialisiert Writes ohnehin.
	db.SetMaxOpenConns(4)

	c := &CostDB{db: db, path: path}
	if err := c.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return c, nil
}

// Close schließt die zugrundeliegende Verbindung.
func (c *CostDB) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

// Path gibt den absoluten Pfad der DB-Datei zurück (für Logging/Debug).
func (c *CostDB) Path() string {
	if c == nil {
		return ""
	}
	return c.path
}

func (c *CostDB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS usage_events (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			ts             INTEGER NOT NULL,
			model          TEXT NOT NULL,
			provider       TEXT NOT NULL,
			channel        TEXT NOT NULL,
			session_id     TEXT NOT NULL DEFAULT '',
			input_tokens   INTEGER NOT NULL DEFAULT 0,
			output_tokens  INTEGER NOT NULL DEFAULT 0,
			total_tokens   INTEGER NOT NULL DEFAULT 0,
			input_cost_usd  REAL NOT NULL DEFAULT 0,
			output_cost_usd REAL NOT NULL DEFAULT 0,
			total_cost_usd  REAL NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_events_ts ON usage_events(ts)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_events_model ON usage_events(model)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_events_provider_channel ON usage_events(provider, channel)`,
		`CREATE TABLE IF NOT EXISTS budget_config (
			id                INTEGER PRIMARY KEY CHECK (id = 1),
			daily_limit_usd   REAL NOT NULL DEFAULT 0,
			monthly_limit_usd REAL NOT NULL DEFAULT 0,
			hard_stop_enabled INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT OR IGNORE INTO budget_config (id, daily_limit_usd, monthly_limit_usd, hard_stop_enabled)
		 VALUES (1, 0, 0, 0)`,
	}
	for _, s := range stmts {
		if _, err := c.db.Exec(s); err != nil {
			return fmt.Errorf("costdb: migration fehlgeschlagen (%q): %w", s, err)
		}
	}
	return nil
}

// **********************************************************************
// Kosten-Berechnung

// CalcCostUSD rechnet Token-Zahlen anhand der Modell-Preise ($/1M Tokens)
// in USD um. Negative/fehlende Preise (0) ergeben 0 Kosten (z.B. Ollama).
// cachedTokens werden, wenn cachedInputCostPerM > 0 bekannt ist (aktuell nur
// cheaperinference liefert einen solchen Cache-Read-Preis), zum günstigeren
// Cache-Satz statt zum normalen InputCost abgerechnet. Ist cachedInputCostPerM
// 0 (kein bekannter Rabatt — Mammouth/Moonshot/ZAI/Longcat), bleibt das
// bisherige Verhalten: alle Input-Tokens voll zum InputCost, weiterhin eine
// obere Schranke.
func CalcCostUSD(inputTokens, outputTokens, cachedTokens int64, inputCostPerM, outputCostPerM, cachedInputCostPerM float64) (inCost, outCost, total float64) {
	if cachedInputCostPerM > 0 && cachedTokens > 0 {
		if cachedTokens > inputTokens {
			cachedTokens = inputTokens
		}
		uncachedTokens := inputTokens - cachedTokens
		inCost = float64(uncachedTokens)/1_000_000.0*inputCostPerM + float64(cachedTokens)/1_000_000.0*cachedInputCostPerM
	} else {
		inCost = float64(inputTokens) / 1_000_000.0 * inputCostPerM
	}
	outCost = float64(outputTokens) / 1_000_000.0 * outputCostPerM
	total = inCost + outCost
	return
}

// **********************************************************************
// Schreiben

// RecordUsage schreibt ein Event. Fire-and-forget-tauglich: der Aufrufer
// sollte einen Fehler nur loggen, nie den API-Response-Pfad daran
// scheitern lassen (Kosten-Tracking darf den Chat-Call nicht blockieren).
func (c *CostDB) RecordUsage(ev UsageEvent) error {
	if c == nil || c.db == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	ts := ev.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	_, err := c.db.Exec(
		`INSERT INTO usage_events
			(ts, model, provider, channel, session_id,
			 input_tokens, output_tokens, total_tokens,
			 input_cost_usd, output_cost_usd, total_cost_usd)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ts.Unix(), ev.Model, ev.Provider, ev.Channel, ev.SessionID,
		ev.InputTokens, ev.OutputTokens, ev.TotalTokens,
		ev.InputCostUSD, ev.OutputCostUSD, ev.TotalCostUSD,
	)
	if err != nil {
		return fmt.Errorf("costdb: insert fehlgeschlagen: %w", err)
	}
	return nil
}

// **********************************************************************
// Lesen / Aggregation

// Summary aggregiert alle Events im Halboffen-Intervall [since, until).
func (c *CostDB) Summary(since, until time.Time) (*CostSummary, error) {
	if c == nil || c.db == nil {
		return &CostSummary{Since: since, Until: until, ByModel: map[string]*CostStat{}, ByChannel: map[string]*CostStat{}}, nil
	}

	sum := &CostSummary{
		Since:     since,
		Until:     until,
		ByModel:   map[string]*CostStat{},
		ByChannel: map[string]*CostStat{},
	}

	rows, err := c.db.Query(
		`SELECT model, provider, channel, input_tokens, output_tokens, total_tokens, total_cost_usd
		 FROM usage_events WHERE ts >= ? AND ts < ?`,
		since.Unix(), ceilUnix(until),
	)
	if err != nil {
		return nil, fmt.Errorf("costdb: query fehlgeschlagen: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var model, provider, channel string
		var inTok, outTok, totTok int64
		var cost float64
		if err := rows.Scan(&model, &provider, &channel, &inTok, &outTok, &totTok, &cost); err != nil {
			return nil, fmt.Errorf("costdb: scan fehlgeschlagen: %w", err)
		}

		sum.Requests++
		sum.InputTokens += inTok
		sum.OutputTokens += outTok
		sum.TotalTokens += totTok
		sum.TotalCostUSD += cost

		ms, ok := sum.ByModel[model]
		if !ok {
			ms = &CostStat{}
			sum.ByModel[model] = ms
		}
		ms.Requests++
		ms.InputTokens += inTok
		ms.OutputTokens += outTok
		ms.TotalTokens += totTok
		ms.TotalCostUSD += cost

		channelKey := fmt.Sprintf("%s#%s", provider, channel)
		cs, ok := sum.ByChannel[channelKey]
		if !ok {
			cs = &CostStat{}
			sum.ByChannel[channelKey] = cs
		}
		cs.Requests++
		cs.InputTokens += inTok
		cs.OutputTokens += outTok
		cs.TotalTokens += totTok
		cs.TotalCostUSD += cost
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("costdb: rows-Iteration fehlgeschlagen: %w", err)
	}
	return sum, nil
}

// spendSince summiert total_cost_usd seit einem Zeitpunkt (für Budget-Checks;
// billiger als Summary(), da kein Go-seitiges Aggregat pro Modell/Kanal nötig ist).
func (c *CostDB) spendSince(since time.Time) (float64, error) {
	if c == nil || c.db == nil {
		return 0, nil
	}
	var total sql.NullFloat64
	err := c.db.QueryRow(
		`SELECT SUM(total_cost_usd) FROM usage_events WHERE ts >= ?`,
		since.Unix(),
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("costdb: spendSince fehlgeschlagen: %w", err)
	}
	return total.Float64, nil
}

// StartOfDay / StartOfMonth — lokale Zeitzone, wie ein Mensch "heute"/"dieser Monat" liest.
func StartOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func StartOfMonth(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, t.Location())
}

// ceilUnix rundet einen Zeitpunkt für den EXKLUSIVEN oberen Rand eines
// Halboffen-Intervalls [since, until) auf die nächste volle Sekunde auf.
//
// Hintergrund: gespeicherte Event-Timestamps werden als ganze Sekunden
// abgelegt (Unix-Sekunden, Nanosekunden-Anteil geht verloren). time.Now()
// hat dagegen fast immer einen Nanosekunden-Anteil > 0. Ohne Aufrundung
// würde until.Unix() (floor) exakt auf die Sekunde fallen, in der ein
// Event GERADE JETZT geschrieben wurde — die strikte "<"-Query würde
// dieses Event dann fälschlich ausschließen (beobachtet: Chat-Call und
// nachfolgender /api/costs-Call im selben Sekunden-Tick → 0 Requests
// trotz frisch geschriebenem Event).
func ceilUnix(t time.Time) int64 {
	u := t.Unix()
	if t.Nanosecond() > 0 {
		u++
	}
	return u
}

// **********************************************************************
// Budget

// GetBudgetConfig liest die aktuelle Budget-Konfiguration.
func (c *CostDB) GetBudgetConfig() (*BudgetConfig, error) {
	cfg := &BudgetConfig{}
	if c == nil || c.db == nil {
		return cfg, nil
	}
	var hardStop int
	err := c.db.QueryRow(
		`SELECT daily_limit_usd, monthly_limit_usd, hard_stop_enabled FROM budget_config WHERE id = 1`,
	).Scan(&cfg.DailyLimitUSD, &cfg.MonthlyLimitUSD, &hardStop)
	if err != nil {
		return nil, fmt.Errorf("costdb: budget-config lesen fehlgeschlagen: %w", err)
	}
	cfg.HardStopEnabled = hardStop != 0
	return cfg, nil
}

// SetBudgetConfig schreibt die Budget-Konfiguration (Upsert der Singleton-Zeile).
func (c *CostDB) SetBudgetConfig(cfg BudgetConfig) error {
	if c == nil || c.db == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	hardStop := 0
	if cfg.HardStopEnabled {
		hardStop = 1
	}
	_, err := c.db.Exec(
		`INSERT INTO budget_config (id, daily_limit_usd, monthly_limit_usd, hard_stop_enabled)
		 VALUES (1, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
			daily_limit_usd = excluded.daily_limit_usd,
			monthly_limit_usd = excluded.monthly_limit_usd,
			hard_stop_enabled = excluded.hard_stop_enabled`,
		cfg.DailyLimitUSD, cfg.MonthlyLimitUSD, hardStop,
	)
	if err != nil {
		return fmt.Errorf("costdb: budget-config schreiben fehlgeschlagen: %w", err)
	}
	return nil
}

// CheckBudget prüft den aktuellen Verbrauch gegen die konfigurierten Limits.
// now wird als Parameter übergeben (statt time.Now() intern), damit Tests
// deterministisch bleiben.
func (c *CostDB) CheckBudget(now time.Time) (*BudgetStatus, error) {
	cfg, err := c.GetBudgetConfig()
	if err != nil {
		return nil, err
	}

	dailySpend, err := c.spendSince(StartOfDay(now))
	if err != nil {
		return nil, err
	}
	monthlySpend, err := c.spendSince(StartOfMonth(now))
	if err != nil {
		return nil, err
	}

	status := &BudgetStatus{
		DailySpendUSD:   dailySpend,
		MonthlySpendUSD: monthlySpend,
		DailyLimitUSD:   cfg.DailyLimitUSD,
		MonthlyLimitUSD: cfg.MonthlyLimitUSD,
		HardStopEnabled: cfg.HardStopEnabled,
	}
	if cfg.DailyLimitUSD > 0 && dailySpend >= cfg.DailyLimitUSD {
		status.DailyExceeded = true
	}
	if cfg.MonthlyLimitUSD > 0 && monthlySpend >= cfg.MonthlyLimitUSD {
		status.MonthlyExceeded = true
	}
	status.Blocked = cfg.HardStopEnabled && (status.DailyExceeded || status.MonthlyExceeded)
	return status, nil
}
