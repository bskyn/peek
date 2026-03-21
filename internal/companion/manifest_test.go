package companion

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCreateManifestSupportsPackageManagers(t *testing.T) {
	tests := []struct {
		name           string
		rootPackage    string
		lockfileName   string
		lockfileData   string
		wantInstallCmd []string
		wantDevCmd     []string
	}{
		{
			name: "pnpm from packageManager",
			rootPackage: `{
  "name": "fixture-web",
  "packageManager": "pnpm@9.0.0",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`,
			wantInstallCmd: []string{"pnpm", "install"},
			wantDevCmd:     []string{"pnpm", "dev"},
		},
		{
			name: "npm without lockfile falls back to install",
			rootPackage: `{
  "name": "fixture-web",
  "packageManager": "npm@10.0.0",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`,
			wantInstallCmd: []string{"npm", "install"},
			wantDevCmd:     []string{"npm", "run", "dev"},
		},
		{
			name: "yarn from lockfile",
			rootPackage: `{
  "name": "fixture-web",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`,
			lockfileName:   "yarn.lock",
			lockfileData:   "__metadata:\n  version: 1\n",
			wantInstallCmd: []string{"yarn", "install"},
			wantDevCmd:     []string{"yarn", "dev"},
		},
		{
			name: "bun from packageManager",
			rootPackage: `{
  "name": "fixture-web",
  "packageManager": "bun@1.2.0",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`,
			wantInstallCmd: []string{"bun", "install"},
			wantDevCmd:     []string{"bun", "run", "dev"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			projectDir := t.TempDir()
			writeRepoFile(t, projectDir, "package.json", tc.rootPackage)
			if tc.lockfileName != "" {
				writeRepoFile(t, projectDir, tc.lockfileName, tc.lockfileData)
			}

			result, err := CreateManifest(ManifestCreateOptions{ProjectDir: projectDir})
			if err != nil {
				t.Fatalf("create manifest: %v", err)
			}
			if result.Template != "node" {
				t.Fatalf("template = %q, want node", result.Template)
			}
			if got := result.Spec.Bootstrap.Commands[0].Command; !reflect.DeepEqual(got, tc.wantInstallCmd) {
				t.Fatalf("install command = %v, want %v", got, tc.wantInstallCmd)
			}
			if got := result.Spec.Services[0].Command; !reflect.DeepEqual(got, tc.wantDevCmd) {
				t.Fatalf("dev command = %v, want %v", got, tc.wantDevCmd)
			}
			if result.Spec.Services[0].Workdir != "" {
				t.Fatalf("expected root service workdir, got %q", result.Spec.Services[0].Workdir)
			}
			if len(result.Rendered) == 0 || result.Rendered[len(result.Rendered)-1] != '\n' {
				t.Fatalf("expected rendered manifest to end with newline")
			}
		})
	}
}

func TestCreateManifestFallsBackToGenericStarter(t *testing.T) {
	projectDir := t.TempDir()
	writeRepoFile(t, projectDir, "go.mod", "module github.com/example/app\n")

	result, err := CreateManifest(ManifestCreateOptions{ProjectDir: projectDir})
	if err != nil {
		t.Fatalf("create generic manifest: %v", err)
	}
	if result.Template != "generic" {
		t.Fatalf("template = %q, want generic", result.Template)
	}
	if len(result.Spec.Services) != 0 {
		t.Fatalf("expected no services in generic starter, got %v", result.Spec.Services)
	}
	if len(result.Warnings) < 2 {
		t.Fatalf("expected generic warnings, got %v", result.Warnings)
	}
}

func TestCreateManifestUsesGenericStarterForMixedRepoByDefault(t *testing.T) {
	projectDir := t.TempDir()
	writeRepoFile(t, projectDir, "package.json", `{
  "name": "fixture-root"
}`)
	writeRepoFile(t, projectDir, "web/package.json", `{
  "name": "peek-web",
  "packageManager": "pnpm@10.0.0",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)
	writeRepoFile(t, projectDir, "web/pnpm-lock.yaml", "lockfileVersion: '9.0'\n")

	result, err := CreateManifest(ManifestCreateOptions{ProjectDir: projectDir})
	if err != nil {
		t.Fatalf("create generic manifest for mixed repo: %v", err)
	}
	if result.Template != "generic" {
		t.Fatalf("template = %q, want generic", result.Template)
	}
	if len(result.Spec.Services) != 0 {
		t.Fatalf("expected no default service, got %v", result.Spec.Services)
	}
}

func TestCreateManifestDetectsExplicitNestedNodeApp(t *testing.T) {
	projectDir := t.TempDir()
	writeRepoFile(t, projectDir, "package.json", `{
  "name": "fixture-root"
}`)
	writeRepoFile(t, projectDir, "web/package.json", `{
  "name": "peek-web",
  "packageManager": "pnpm@10.0.0",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)
	writeRepoFile(t, projectDir, "web/pnpm-lock.yaml", "lockfileVersion: '9.0'\n")

	result, err := CreateManifest(ManifestCreateOptions{
		ProjectDir:  projectDir,
		ServicePath: "web",
	})
	if err != nil {
		t.Fatalf("create manifest for explicit nested node app: %v", err)
	}
	if result.Template != "node" {
		t.Fatalf("template = %q, want node", result.Template)
	}
	if got := result.Selected.Path; got != "web" {
		t.Fatalf("selected path = %q, want web", got)
	}
	if got := result.Spec.Bootstrap.Commands[0].Workdir; got != "web" {
		t.Fatalf("bootstrap workdir = %q, want web", got)
	}
	if got := result.Spec.Services[0].Workdir; got != "web" {
		t.Fatalf("service workdir = %q, want web", got)
	}
	if got := strings.Join(result.Spec.Services[0].Command, " "); got != "pnpm dev" {
		t.Fatalf("service command = %q, want pnpm dev", got)
	}
	if got := result.Spec.Bootstrap.FingerprintPaths; !reflect.DeepEqual(got, []string{"web/pnpm-lock.yaml", "package.json", "web/package.json"}) {
		t.Fatalf("fingerprint paths = %v", got)
	}
}

func TestCreateManifestRequiresServiceForAmbiguousWorkspace(t *testing.T) {
	projectDir := t.TempDir()
	writeRepoFile(t, projectDir, "package.json", `{
  "name": "fixture-root",
  "packageManager": "pnpm@9.0.0"
}`)
	writeRepoFile(t, projectDir, "pnpm-workspace.yaml", "packages:\n  - apps/*\n")
	writeRepoFile(t, projectDir, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
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
	writeRepoFile(t, projectDir, "apps/core/.env.local", "HELLO=world\n")

	_, err := CreateManifest(ManifestCreateOptions{ProjectDir: projectDir})
	var ambiguous *AmbiguousManifestError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
	if got, want := candidatePaths(ambiguous.Candidates), []string{"apps/core", "apps/xyz"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate paths = %v, want %v", got, want)
	}

	first, err := CreateManifest(ManifestCreateOptions{
		ProjectDir:  projectDir,
		ServicePath: "apps/core",
	})
	if err != nil {
		t.Fatalf("create manifest with service override: %v", err)
	}
	second, err := CreateManifest(ManifestCreateOptions{
		ProjectDir:  projectDir,
		ServicePath: "apps/core",
	})
	if err != nil {
		t.Fatalf("create manifest with service override again: %v", err)
	}

	if !bytes.Equal(first.Rendered, second.Rendered) {
		t.Fatal("expected deterministic manifest rendering")
	}
	if got := first.Spec.Services[0].Workdir; got != "apps/core" {
		t.Fatalf("workdir = %q, want apps/core", got)
	}
	if got := strings.Join(first.Spec.Services[0].Command, " "); got != "pnpm dev" {
		t.Fatalf("dev command = %q, want pnpm dev", got)
	}
	if got := first.Spec.Services[0].Name; got != "core" {
		t.Fatalf("service name = %q, want core", got)
	}
	if got := first.Spec.EnvSources[0].Path; got != "apps/core/.env.local" {
		t.Fatalf("env source = %q, want apps/core/.env.local", got)
	}
	if got := first.Spec.Bootstrap.FingerprintPaths; !reflect.DeepEqual(got, []string{"pnpm-lock.yaml", "package.json", "apps/core/package.json"}) {
		t.Fatalf("fingerprint paths = %v", got)
	}
}

func TestCreateManifestSupportsRootAppInsideWorkspaceRepo(t *testing.T) {
	projectDir := t.TempDir()
	writeRepoFile(t, projectDir, "package.json", `{
  "name": "root-web",
  "packageManager": "pnpm@9.0.0",
  "scripts": {"dev": "next dev"},
  "dependencies": {"next": "^16.0.0"},
  "workspaces": ["apps/*"]
}`)
	writeRepoFile(t, projectDir, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	writeRepoFile(t, projectDir, "apps/docs/package.json", `{
  "name": "docs",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)

	_, err := CreateManifest(ManifestCreateOptions{ProjectDir: projectDir})
	var ambiguous *AmbiguousManifestError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
	got := candidatePaths(ambiguous.Candidates)
	if len(got) != 2 {
		t.Fatalf("candidate paths = %v, want 2 candidates", got)
	}
	if !(reflect.DeepEqual(got, []string{".", "apps/docs"}) || reflect.DeepEqual(got, []string{"apps/docs", "."})) {
		t.Fatalf("candidate paths = %v, want root and apps/docs", got)
	}

	result, err := CreateManifest(ManifestCreateOptions{
		ProjectDir:  projectDir,
		ServicePath: ".",
	})
	if err != nil {
		t.Fatalf("create manifest for root app: %v", err)
	}
	if got := result.Spec.Services[0].Workdir; got != "" {
		t.Fatalf("workdir = %q, want root", got)
	}
	if got := result.Spec.Services[0].Ready.URL; got != "http://127.0.0.1:3000/" {
		t.Fatalf("ready url = %q, want next default", got)
	}
}

func TestCreateManifestHonorsPNPMWorkspaceExcludes(t *testing.T) {
	projectDir := t.TempDir()
	writeRepoFile(t, projectDir, "package.json", `{
  "name": "fixture-root",
  "packageManager": "pnpm@9.0.0"
}`)
	writeRepoFile(t, projectDir, "pnpm-workspace.yaml", "packages:\n  - apps/*\n  - '!apps/legacy'\n")
	writeRepoFile(t, projectDir, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	writeRepoFile(t, projectDir, "apps/core/package.json", `{
  "name": "core",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)
	writeRepoFile(t, projectDir, "apps/legacy/package.json", `{
  "name": "legacy",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)

	result, err := CreateManifest(ManifestCreateOptions{ProjectDir: projectDir})
	if err != nil {
		t.Fatalf("create manifest with excludes: %v", err)
	}
	if got := result.Selected.Path; got != "apps/core" {
		t.Fatalf("selected path = %q, want apps/core", got)
	}
}

func TestGeneratedManifestRoundTripsThroughResolveProjectRuntime(t *testing.T) {
	projectDir := t.TempDir()
	writeRepoFile(t, projectDir, "package.json", `{
  "name": "fixture-root",
  "packageManager": "pnpm@9.0.0"
}`)
	writeRepoFile(t, projectDir, "pnpm-workspace.yaml", "packages:\n  - apps/*\n")
	writeRepoFile(t, projectDir, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	writeRepoFile(t, projectDir, "apps/core/package.json", `{
  "name": "core",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)

	result, err := CreateManifest(ManifestCreateOptions{
		ProjectDir:  projectDir,
		ServicePath: "apps/core",
	})
	if err != nil {
		t.Fatalf("create manifest: %v", err)
	}
	writeRepoFile(t, projectDir, ConfigFileName, string(result.Rendered))

	spec, err := ResolveProjectRuntime(projectDir)
	if err != nil {
		t.Fatalf("resolve project runtime: %v", err)
	}
	if !strings.HasSuffix(spec.ConfigSource, ConfigFileName) {
		t.Fatalf("config source = %q, want %s suffix", spec.ConfigSource, ConfigFileName)
	}
	if got := spec.Services[0].Workdir; got != "apps/core" {
		t.Fatalf("workdir = %q, want apps/core", got)
	}
	if got := strings.Join(spec.Services[0].Command, " "); got != "pnpm dev" {
		t.Fatalf("command = %q, want pnpm dev", got)
	}
}

func writeRepoFile(t *testing.T, root, relPath, contents string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", fullPath, err)
	}
	if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", fullPath, err)
	}
}
