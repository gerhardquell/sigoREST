package sigoengine

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// **********************************************************************
// Sync + Retire-Logik Tests (Task 4)

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

// **********************************************************************
// Shortcode-Auflösung Tests (Task 5)

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
