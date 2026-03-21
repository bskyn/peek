package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestCreateStdoutOnlyPrintsManifest(t *testing.T) {
	projectDir := t.TempDir()
	writeCLITestFile(t, projectDir, "package.json", `{
  "name": "fixture-web",
  "packageManager": "pnpm@9.0.0",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)

	output, err := runRootCommandForTest(t, projectDir, "manifest", "create", "--stdout")
	if err != nil {
		t.Fatalf("run manifest create --stdout: %v", err)
	}
	if !strings.HasPrefix(output, "{\n") {
		t.Fatalf("expected JSON output, got %q", output)
	}
	if strings.Contains(output, "Created peek.runtime.json") {
		t.Fatalf("expected stdout mode to omit status text, got %q", output)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "peek.runtime.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no file write, stat err = %v", err)
	}
}

func TestManifestCreateRejectsExistingFileWithoutForce(t *testing.T) {
	projectDir := t.TempDir()
	writeCLITestFile(t, projectDir, "package.json", `{
  "name": "fixture-web",
  "packageManager": "pnpm@9.0.0",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)
	writeCLITestFile(t, projectDir, "peek.runtime.json", "{}\n")

	_, err := runRootCommandForTest(t, projectDir, "manifest", "create")
	if err == nil {
		t.Fatal("expected overwrite refusal")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManifestCreateRejectsForceWithStdout(t *testing.T) {
	projectDir := t.TempDir()
	writeCLITestFile(t, projectDir, "package.json", `{
  "name": "fixture-web",
  "packageManager": "pnpm@9.0.0",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)

	_, err := runRootCommandForTest(t, projectDir, "manifest", "create", "--stdout", "--force")
	if err == nil {
		t.Fatal("expected flag validation error")
	}
	if !strings.Contains(err.Error(), "--force cannot be used with --stdout") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManifestCreateListsCandidatesForAmbiguousWorkspace(t *testing.T) {
	projectDir := t.TempDir()
	writeCLITestFile(t, projectDir, "package.json", `{
  "name": "fixture-root",
  "packageManager": "pnpm@9.0.0"
}`)
	writeCLITestFile(t, projectDir, "pnpm-workspace.yaml", "packages:\n  - apps/*\n")
	writeCLITestFile(t, projectDir, "apps/core/package.json", `{
  "name": "core",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)
	writeCLITestFile(t, projectDir, "apps/xyz/package.json", `{
  "name": "xyz",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)

	_, err := runRootCommandForTest(t, projectDir, "manifest", "create")
	if err == nil {
		t.Fatal("expected ambiguous candidate error")
	}
	if !strings.Contains(err.Error(), "--service <path>") {
		t.Fatalf("expected rerun hint, got %v", err)
	}
	if !strings.Contains(err.Error(), "apps/core") || !strings.Contains(err.Error(), "apps/xyz") {
		t.Fatalf("expected candidate list, got %v", err)
	}
	if !strings.Contains(err.Error(), "peek manifest create --service apps/core") {
		t.Fatalf("expected concrete example, got %v", err)
	}
}

func TestManifestCreateReportsRootAppTarget(t *testing.T) {
	projectDir := t.TempDir()
	writeCLITestFile(t, projectDir, "package.json", `{
  "name": "root-web",
  "packageManager": "pnpm@9.0.0",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)

	output, err := runRootCommandForTest(t, projectDir, "manifest", "create")
	if err != nil {
		t.Fatalf("run manifest create: %v", err)
	}
	if !strings.Contains(output, "Created peek.runtime.json for the repo root app (root-web).") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestManifestCreateFromSubdirUsesRepoRoot(t *testing.T) {
	projectDir := t.TempDir()
	writeCLITestFile(t, projectDir, ".git/HEAD", "ref: refs/heads/main\n")
	writeCLITestFile(t, projectDir, "package.json", `{
  "name": "fixture-root",
  "packageManager": "pnpm@9.0.0"
}`)
	writeCLITestFile(t, projectDir, "pnpm-workspace.yaml", "packages:\n  - apps/*\n")
	writeCLITestFile(t, projectDir, "apps/core/package.json", `{
  "name": "core",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^6.0.0"}
}`)

	subdir := filepath.Join(projectDir, "apps", "core")
	expectedRoot, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		expectedRoot = projectDir
	}
	expectedSubdir, err := filepath.EvalSymlinks(subdir)
	if err != nil {
		expectedSubdir = subdir
	}
	output, err := runRootCommandForTest(t, subdir, "manifest", "create")
	if err != nil {
		t.Fatalf("run manifest create from subdir: %v", err)
	}
	if !strings.Contains(output, "Using repo root "+expectedRoot+" (invoked from "+expectedSubdir+").") {
		t.Fatalf("expected repo root note, got %q", output)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "peek.runtime.json")); err != nil {
		t.Fatalf("expected manifest at repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(subdir, "peek.runtime.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no manifest in subdir, stat err = %v", err)
	}
}

func runRootCommandForTest(t *testing.T, cwd string, args ...string) (string, error) {
	t.Helper()

	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir %s: %v", cwd, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(previousDir)
	})

	cmd := newRootCmd()
	cmd.SetArgs(args)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	err = cmd.Execute()
	return stdout.String(), err
}

func writeCLITestFile(t *testing.T, root, relPath, contents string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", fullPath, err)
	}
	if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", fullPath, err)
	}
}
