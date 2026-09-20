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
