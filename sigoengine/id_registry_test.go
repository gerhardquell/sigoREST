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
