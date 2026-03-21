package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bskyn/peek/internal/companion"
)

func TestRenderMissingProjectRuntimeMessageForRootApp(t *testing.T) {
	message := renderMissingProjectRuntimeMessage(&companion.MissingProjectRuntimeError{
		ProjectDir: "/repo",
		Candidates: []companion.ServiceCandidate{
			{Path: "", PackageName: "root-web"},
		},
	}, &projectRootResolution{CWD: "/repo", ProjectRoot: "/repo", IsRepoRoot: true})

	for _, want := range []string{
		"Managed runs require an explicit peek.runtime.json",
		"Create a generic manifest:",
		"peek manifest create",
		"Peek found one likely app candidate: . (root-web)",
		"peek run claude",
		"peek run codex",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected message to contain %q, got:\n%s", want, message)
		}
	}
}

func TestRenderMissingProjectRuntimeMessageForMonorepo(t *testing.T) {
	message := renderMissingProjectRuntimeMessage(&companion.MissingProjectRuntimeError{
		ProjectDir: "/repo",
		Candidates: []companion.ServiceCandidate{
			{Path: "apps/core", PackageName: "core"},
			{Path: "apps/xyz", PackageName: "xyz"},
		},
	}, &projectRootResolution{CWD: "/repo", ProjectRoot: "/repo", IsRepoRoot: true})

	for _, want := range []string{
		"multiple app candidates",
		"apps/core (core)",
		"apps/xyz (xyz)",
		"peek manifest create --service <path>",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected message to contain %q, got:\n%s", want, message)
		}
	}
}

func TestRenderMissingProjectRuntimeMessageForGenericRepo(t *testing.T) {
	message := renderMissingProjectRuntimeMessage(&companion.MissingProjectRuntimeError{
		ProjectDir: "/repo",
	}, &projectRootResolution{CWD: "/repo", ProjectRoot: "/repo", IsRepoRoot: true})

	for _, want := range []string{
		"Managed runs require an explicit peek.runtime.json",
		"Create a generic manifest:",
		"peek manifest create",
		"peek run claude",
		"peek run codex",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected message to contain %q, got:\n%s", want, message)
		}
	}
}

func TestRunManagedFromSubdirUsesRepoRootInError(t *testing.T) {
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
	expectedRoot, evalErr := filepath.EvalSymlinks(projectDir)
	if evalErr != nil {
		expectedRoot = projectDir
	}
	expectedSubdir, evalErr := filepath.EvalSymlinks(subdir)
	if evalErr != nil {
		expectedSubdir = subdir
	}
	_, err := runRootCommandForTest(t, subdir, "run", "claude")
	if err == nil {
		t.Fatal("expected missing manifest error")
	}
	for _, want := range []string{
		"Peek detected the repo root at " + expectedRoot,
		"(invoked from " + expectedSubdir + ")",
		"peek manifest create",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got:\n%s", want, err.Error())
		}
	}
}
