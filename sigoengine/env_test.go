package sigoengine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
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

// TestNoDirectOsGetenv: API-Keys müssen über GetEnvWithFile gelesen werden,
// sonst werden Keys, die nur in ./.env stehen, ignoriert. Bis 2026-09-27
// lasen die Provider-Fetcher os.Getenv direkt: bei manuellem Start mit Keys
// nur in .env bekamen die Kanäle ihre Keys, der Modellabruf aber nicht
// (Moonshot/cheaperinference → 0 Modelle). Einzige erlaubte Stelle: env.go.
func TestNoDirectOsGetenv(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if f == "env.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		node, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(node, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Getenv" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" {
				t.Errorf("%s: os.Getenv statt GetEnvWithFile", fset.Position(sel.Pos()))
			}
			return true
		})
	}
}
