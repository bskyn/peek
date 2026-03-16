package companion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProjectRuntimeFromConfig(t *testing.T) {
	projectDir := filepath.Join("testdata", "monorepo")

	spec, err := ResolveProjectRuntime(projectDir)
	if err != nil {
		t.Fatalf("resolve project runtime: %v", err)
	}
	if spec == nil {
		t.Fatal("expected explicit config runtime")
	}
	if !strings.HasSuffix(spec.ConfigSource, ConfigFileName) {
		t.Fatalf("expected config source to end with %s, got %s", ConfigFileName, spec.ConfigSource)
	}
	if len(spec.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(spec.Services))
	}
	if spec.Services[0].Name != "web" || spec.Services[0].Role != ServiceRolePrimary {
		t.Fatalf("unexpected service: %+v", spec.Services[0])
	}
}

func TestResolveProjectRuntimeAutodetectsFrontend(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "pnpm-lock.yaml"), []byte("lockfileVersion: 9"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "package.json"), []byte(`{
  "name": "fixture-root",
  "private": true,
  "workspaces": ["apps/*"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "apps", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "apps", "web", "package.json"), []byte(`{
  "name": "fixture-web",
  "scripts": {"dev": "vite"},
  "dependencies": {"react": "^19.0.0", "vite": "^6.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "apps", "web", ".env.local"), []byte("HELLO=world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec, err := ResolveProjectRuntime(projectDir)
	if err != nil {
		t.Fatalf("resolve autodetected runtime: %v", err)
	}
	if spec == nil {
		t.Fatal("expected autodetected runtime")
	}
	if spec.ConfigSource != "autodetect" {
		t.Fatalf("expected autodetect source, got %s", spec.ConfigSource)
	}
	if got := strings.Join(spec.Services[0].Command, " "); got != "pnpm --dir apps/web dev" {
		t.Fatalf("unexpected dev command: %s", got)
	}
	if spec.Services[0].Ready.URL != "http://127.0.0.1:5173/" {
		t.Fatalf("unexpected readiness url: %s", spec.Services[0].Ready.URL)
	}
	if len(spec.EnvSources) != 1 || spec.EnvSources[0].Path != filepath.Join("apps", "web", ".env.local") {
		t.Fatalf("unexpected env sources: %+v", spec.EnvSources)
	}
}

func TestResolveProjectRuntimeSkipsNestedPackageWithoutWorkspaceDeclaration(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "pnpm-lock.yaml"), []byte("lockfileVersion: 9"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "package.json"), []byte(`{
  "name": "fixture-root",
  "private": true
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "web", "package.json"), []byte(`{
  "name": "fixture-web",
  "scripts": {"dev": "vite"},
  "dependencies": {"react": "^19.0.0", "vite": "^6.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	spec, err := ResolveProjectRuntime(projectDir)
	if err != nil {
		t.Fatalf("resolve runtime: %v", err)
	}
	if spec != nil {
		t.Fatalf("expected nested package without workspaces to be ignored, got %+v", spec)
	}
}
