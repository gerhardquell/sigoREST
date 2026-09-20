//**********************************************************************
//      sigoREST/costhandlers.go
//**********************************************************************
//  Autor    : Gerhard Quell - gquell@skequell.de
//  CoAutor  : Claude (Hermes-Session, sigoREST-Kosten-Tracking)
//  Copyright: 2025 Gerhard Quell - SKEQuell
//**********************************************************************
//  HTTP-Handler für persistentes Kosten-Tracking (costs.db):
//    GET  /api/costs          — Zusammenfassung über einen Zeitraum
//    GET  /api/budget         — aktuelle Budget-Config + Verbrauchsstatus
//    PUT  /api/budget         — Budget-Limits setzen
//**********************************************************************

package main

import (
	"encoding/json"
	"net/http"
	"time"

	"sigorest/sigoengine"
)

// **********************************************************************
// GET /api/costs?since=RFC3339&until=RFC3339
//
// Ohne Parameter: Zusammenfassung des laufenden Tages (lokale Zeit).
// since/until akzeptieren RFC3339 ("2026-09-01T00:00:00+02:00") oder das
// kurze Datumsformat "2026-09-01" (Mitternacht lokal angenommen).
func (s *Server) handleCosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.costDB == nil {
		writeError(w, "Kosten-Tracking ist nicht aktiv (costs.db konnte beim Start nicht geöffnet werden)", "cost_tracking_disabled", http.StatusServiceUnavailable)
		return
	}

	now := time.Now()
	since := sigoengine.StartOfDay(now)
	until := now

	if v := r.URL.Query().Get("since"); v != "" {
		parsed, err := parseFlexibleTime(v)
		if err != nil {
			writeError(w, "Ungültiger 'since'-Parameter: "+err.Error(), "invalid_request", http.StatusBadRequest)
			return
		}
		since = parsed
	}
	if v := r.URL.Query().Get("until"); v != "" {
		parsed, err := parseFlexibleTime(v)
		if err != nil {
			writeError(w, "Ungültiger 'until'-Parameter: "+err.Error(), "invalid_request", http.StatusBadRequest)
			return
		}
		until = parsed
	}

	// Bequemlichkeits-Shortcuts, überschreiben since/until falls gesetzt.
	switch r.URL.Query().Get("period") {
	case "today":
		since, until = sigoengine.StartOfDay(now), now
	case "month":
		since, until = sigoengine.StartOfMonth(now), now
	}

	summary, err := s.costDB.Summary(since, until)
	if err != nil {
		writeError(w, err.Error(), "internal_error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summary)
}

// parseFlexibleTime akzeptiert RFC3339 oder "YYYY-MM-DD" (dann 00:00 lokal).
func parseFlexibleTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", v, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, &time.ParseError{Layout: "RFC3339 oder YYYY-MM-DD", Value: v}
}

// **********************************************************************
// GET/PUT /api/budget
//
// GET liefert Config + aktuellen Verbrauchsstatus (Tag/Monat) in einer
// Antwort — der übliche erste Blick braucht keinen zweiten Request.
// PUT setzt daily_limit_usd / monthly_limit_usd / hard_stop_enabled.
func (s *Server) handleBudget(w http.ResponseWriter, r *http.Request) {
	if s.costDB == nil {
		writeError(w, "Kosten-Tracking ist nicht aktiv (costs.db konnte beim Start nicht geöffnet werden)", "cost_tracking_disabled", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		cfg, err := s.costDB.GetBudgetConfig()
		if err != nil {
			writeError(w, err.Error(), "internal_error", http.StatusInternalServerError)
			return
		}
		status, err := s.costDB.CheckBudget(time.Now())
		if err != nil {
			writeError(w, err.Error(), "internal_error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"config": cfg,
			"status": status,
		})

	case http.MethodPut:
		var cfg sigoengine.BudgetConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeError(w, "Invalid JSON: "+err.Error(), "invalid_request", http.StatusBadRequest)
			return
		}
		if cfg.DailyLimitUSD < 0 || cfg.MonthlyLimitUSD < 0 {
			writeError(w, "Limits dürfen nicht negativ sein", "invalid_request", http.StatusBadRequest)
			return
		}
		if err := s.costDB.SetBudgetConfig(cfg); err != nil {
			writeError(w, err.Error(), "internal_error", http.StatusInternalServerError)
			return
		}
		sigoengine.LogInfo("Budget-Konfiguration geändert", map[string]interface{}{
			"daily_limit_usd": cfg.DailyLimitUSD, "monthly_limit_usd": cfg.MonthlyLimitUSD,
			"hard_stop_enabled": cfg.HardStopEnabled,
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
