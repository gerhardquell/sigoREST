package sigoengine

import (
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
