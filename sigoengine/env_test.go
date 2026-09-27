package sigoengine

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnvFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("FOO=bar\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

// TestResolveEnvFile_NoFile: weder .env noch env → nichts laden, keine Warnung.
func TestResolveEnvFile_NoFile(t *testing.T) {
	dir := t.TempDir()
	path, warn := ResolveEnvFile(dir)
	if path != "" || warn != "" {
		t.Fatalf("erwartet (\"\", \"\"), bekommen (%q, %q)", path, warn)
	}
}

// TestResolveEnvFile_DotEnvOnly: Normalfall — .env laden, keine Warnung.
func TestResolveEnvFile_DotEnvOnly(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, DotEnvFileName)
	path, warn := ResolveEnvFile(dir)
	if path != filepath.Join(dir, DotEnvFileName) {
		t.Fatalf("path = %q, erwartet .env", path)
	}
	if warn != "" {
		t.Fatalf("unerwartete Warnung: %q", warn)
	}
}

// TestResolveEnvFile_LegacyOnly: nur alte ./env → trotzdem laden, aber warnen.
func TestResolveEnvFile_LegacyOnly(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, LegacyEnvFileName)
	path, warn := ResolveEnvFile(dir)
	if path != filepath.Join(dir, LegacyEnvFileName) {
		t.Fatalf("path = %q, erwartet env", path)
	}
	if warn == "" {
		t.Fatal("Warnung für veraltete ./env erwartet")
	}
}

// TestResolveEnvFile_BothPreferDotEnv: beide vorhanden → .env gewinnt,
// aber warnen, damit die vergessene ./env (Klartext-Keys) gelöscht wird.
func TestResolveEnvFile_BothPreferDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, DotEnvFileName)
	writeEnvFile(t, dir, LegacyEnvFileName)
	path, warn := ResolveEnvFile(dir)
	if path != filepath.Join(dir, DotEnvFileName) {
		t.Fatalf("path = %q, erwartet .env", path)
	}
	if warn == "" {
		t.Fatal("Warnung für ignorierte ./env erwartet")
	}
}
