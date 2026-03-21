package companion

import (
	"errors"
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

func TestResolveProjectRuntimeRequiresManifestForRootApp(t *testing.T) {
	projectDir := t.TempDir()
	writeRepoFile(t, projectDir, "package.json", `{
  "name": "fixture-web",
  "packageManager": "pnpm@9.0.0",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)

	_, err := ResolveProjectRuntime(projectDir)
	var missing *MissingProjectRuntimeError
	if !errors.As(err, &missing) {
		t.Fatalf("expected missing manifest error, got %v", err)
	}
	if len(missing.Candidates) != 1 || missing.Candidates[0].Path != "" {
		t.Fatalf("unexpected candidates: %+v", missing.Candidates)
	}
}

func TestResolveProjectRuntimeRequiresManifestForMonorepo(t *testing.T) {
	projectDir := t.TempDir()
	writeRepoFile(t, projectDir, "package.json", `{
  "name": "fixture-root",
  "packageManager": "pnpm@9.0.0"
}`)
	writeRepoFile(t, projectDir, "pnpm-workspace.yaml", "packages:\n  - apps/*\n")
	writeRepoFile(t, projectDir, "apps/core/package.json", `{
  "name": "core",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)
	writeRepoFile(t, projectDir, "apps/xyz/package.json", `{
  "name": "xyz",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)

	_, err := ResolveProjectRuntime(projectDir)
	var missing *MissingProjectRuntimeError
	if !errors.As(err, &missing) {
		t.Fatalf("expected missing manifest error, got %v", err)
	}
	if got, want := candidatePaths(missing.Candidates), []string{"apps/core", "apps/xyz"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("candidate paths = %v, want %v", got, want)
	}
}
