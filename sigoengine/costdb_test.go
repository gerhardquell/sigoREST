//**********************************************************************
//      sigoengine/costdb_test.go
//**********************************************************************

package sigoengine

import (
	"testing"
	"time"
)

func newTestCostDB(t *testing.T) *CostDB {
	t.Helper()
	dir := t.TempDir()
	db, err := OpenCostDB(dir)
	if err != nil {
		t.Fatalf("OpenCostDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCostDB_CreatesSchemaAndDefaultBudget(t *testing.T) {
	db := newTestCostDB(t)

	cfg, err := db.GetBudgetConfig()
	if err != nil {
		t.Fatalf("GetBudgetConfig: %v", err)
	}
	if cfg.DailyLimitUSD != 0 || cfg.MonthlyLimitUSD != 0 || cfg.HardStopEnabled {
		t.Fatalf("erwarte leere Default-Budget-Config, bekam %+v", cfg)
	}
}

func TestCalcCostUSD(t *testing.T) {
	in, out, total := CalcCostUSD(1_000_000, 500_000, 2.0, 8.0)
	if in != 2.0 {
		t.Errorf("input cost = %.4f, erwartet 2.0", in)
	}
	if out != 4.0 {
		t.Errorf("output cost = %.4f, erwartet 4.0", out)
	}
	if total != 6.0 {
		t.Errorf("total cost = %.4f, erwartet 6.0", total)
	}
}

func TestCalcCostUSD_ZeroPriceForFreeModels(t *testing.T) {
	in, out, total := CalcCostUSD(100_000, 50_000, 0, 0)
	if in != 0 || out != 0 || total != 0 {
		t.Errorf("erwarte 0-Kosten für Ollama-artige Modelle, bekam in=%.4f out=%.4f total=%.4f", in, out, total)
	}
}

func TestRecordUsage_And_Summary(t *testing.T) {
	db := newTestCostDB(t)
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

	events := []UsageEvent{
		{Timestamp: now, Model: "claude-h", Provider: "mammouth", Channel: "default", InputTokens: 100, OutputTokens: 50, TotalTokens: 150, TotalCostUSD: 0.001},
		{Timestamp: now.Add(time.Minute), Model: "claude-h", Provider: "mammouth", Channel: "default", InputTokens: 200, OutputTokens: 100, TotalTokens: 300, TotalCostUSD: 0.002},
		{Timestamp: now.Add(time.Minute), Model: "kimi", Provider: "moonshot", Channel: "default", InputTokens: 50, OutputTokens: 25, TotalTokens: 75, TotalCostUSD: 0.0005},
	}
	for _, ev := range events {
		if err := db.RecordUsage(ev); err != nil {
			t.Fatalf("RecordUsage: %v", err)
		}
	}

	sum, err := db.Summary(now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if sum.Requests != 3 {
		t.Errorf("Requests = %d, erwartet 3", sum.Requests)
	}
	if sum.TotalTokens != 525 {
		t.Errorf("TotalTokens = %d, erwartet 525", sum.TotalTokens)
	}
	wantCost := 0.001 + 0.002 + 0.0005
	if diff := sum.TotalCostUSD - wantCost; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("TotalCostUSD = %.6f, erwartet %.6f", sum.TotalCostUSD, wantCost)
	}
	if ms, ok := sum.ByModel["claude-h"]; !ok || ms.Requests != 2 {
		t.Errorf("ByModel[claude-h] = %+v, erwarte Requests=2", ms)
	}
	if cs, ok := sum.ByChannel["mammouth#default"]; !ok || cs.Requests != 2 {
		t.Errorf("ByChannel[mammouth#default] = %+v, erwarte Requests=2", cs)
	}
}

func TestSummary_RespectsTimeWindow(t *testing.T) {
	db := newTestCostDB(t)
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

	// Ein Event gestern, eins heute.
	_ = db.RecordUsage(UsageEvent{Timestamp: now.Add(-24 * time.Hour), Model: "m", Provider: "p", Channel: "c", TotalCostUSD: 1.0})
	_ = db.RecordUsage(UsageEvent{Timestamp: now, Model: "m", Provider: "p", Channel: "c", TotalCostUSD: 2.0})

	sum, err := db.Summary(StartOfDay(now), StartOfDay(now).Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if sum.Requests != 1 {
		t.Fatalf("Requests = %d, erwartet 1 (nur heutiges Event)", sum.Requests)
	}
	if sum.TotalCostUSD != 2.0 {
		t.Fatalf("TotalCostUSD = %.4f, erwartet 2.0", sum.TotalCostUSD)
	}
}

// TestSummary_IncludesEventFromSameInstantAsUntil reproduziert einen
// Off-by-One-Bug: ein Event, das in derselben Sekunde geschrieben wird, in
// der `until` (z.B. time.Now() im /api/costs-Handler) berechnet wird, darf
// nicht durch Sekunden-Rundung aus dem Fenster fallen. until trägt hier
// bewusst einen Nanosekunden-Anteil > 0, exakt wie ein echtes time.Now().
func TestSummary_IncludesEventFromSameInstantAsUntil(t *testing.T) {
	db := newTestCostDB(t)
	now := time.Date(2026, 9, 19, 17, 48, 57, 0, time.UTC) // volle Sekunde, wie gespeicherte Events

	if err := db.RecordUsage(UsageEvent{Timestamp: now, Model: "m", Provider: "p", Channel: "c", TotalCostUSD: 1.0}); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	until := now.Add(879664 * time.Microsecond) // "jetzt" mit Nanosekunden-Rest, wie time.Now()
	sum, err := db.Summary(StartOfDay(now), until)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if sum.Requests != 1 {
		t.Fatalf("Requests = %d, erwartet 1 — Event aus derselben Sekunde wie 'until' fehlt", sum.Requests)
	}
}

func TestBudget_SetAndGet(t *testing.T) {
	db := newTestCostDB(t)

	want := BudgetConfig{DailyLimitUSD: 5.0, MonthlyLimitUSD: 100.0, HardStopEnabled: true}
	if err := db.SetBudgetConfig(want); err != nil {
		t.Fatalf("SetBudgetConfig: %v", err)
	}
	got, err := db.GetBudgetConfig()
	if err != nil {
		t.Fatalf("GetBudgetConfig: %v", err)
	}
	if *got != want {
		t.Fatalf("GetBudgetConfig = %+v, erwartet %+v", got, want)
	}
}

func TestCheckBudget_WarningWithoutHardStop(t *testing.T) {
	db := newTestCostDB(t)
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

	if err := db.SetBudgetConfig(BudgetConfig{DailyLimitUSD: 1.0, HardStopEnabled: false}); err != nil {
		t.Fatalf("SetBudgetConfig: %v", err)
	}
	if err := db.RecordUsage(UsageEvent{Timestamp: now, Model: "m", Provider: "p", Channel: "c", TotalCostUSD: 2.0}); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	status, err := db.CheckBudget(now)
	if err != nil {
		t.Fatalf("CheckBudget: %v", err)
	}
	if !status.DailyExceeded {
		t.Error("erwarte DailyExceeded=true (2.0 >= Limit 1.0)")
	}
	if status.Blocked {
		t.Error("erwarte Blocked=false, da HardStopEnabled=false — nur Warnung, kein Cutoff")
	}
}

func TestCheckBudget_HardStopBlocks(t *testing.T) {
	db := newTestCostDB(t)
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

	if err := db.SetBudgetConfig(BudgetConfig{DailyLimitUSD: 1.0, HardStopEnabled: true}); err != nil {
		t.Fatalf("SetBudgetConfig: %v", err)
	}
	if err := db.RecordUsage(UsageEvent{Timestamp: now, Model: "m", Provider: "p", Channel: "c", TotalCostUSD: 2.0}); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	status, err := db.CheckBudget(now)
	if err != nil {
		t.Fatalf("CheckBudget: %v", err)
	}
	if !status.Blocked {
		t.Error("erwarte Blocked=true, da HardStopEnabled=true und Limit überschritten")
	}
}

func TestCheckBudget_NoLimitNeverBlocks(t *testing.T) {
	db := newTestCostDB(t)
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

	// Kein SetBudgetConfig-Aufruf → Defaults (0 = kein Limit).
	if err := db.RecordUsage(UsageEvent{Timestamp: now, Model: "m", Provider: "p", Channel: "c", TotalCostUSD: 999.0}); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	status, err := db.CheckBudget(now)
	if err != nil {
		t.Fatalf("CheckBudget: %v", err)
	}
	if status.DailyExceeded || status.MonthlyExceeded || status.Blocked {
		t.Errorf("erwarte kein Limit-Trigger bei Limit=0, bekam %+v", status)
	}
}

func TestRecordUsage_NilDBIsNoop(t *testing.T) {
	var db *CostDB
	if err := db.RecordUsage(UsageEvent{Model: "m"}); err != nil {
		t.Fatalf("RecordUsage auf nil-DB soll no-op sein, bekam Fehler: %v", err)
	}
}
