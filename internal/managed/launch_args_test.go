package managed

import (
	"reflect"
	"strings"
	"testing"
)

func TestPrepareManagedLaunchArgsAllowsReusableOptions(t *testing.T) {
	tests := []struct {
		name   string
		source Source
		args   []string
	}{
		{
			name:   "claude flags",
			source: SourceClaude,
			args:   []string{"--model", "sonnet", "--debug=api", "--permission-mode", "plan"},
		},
		{
			name:   "codex flags",
			source: SourceCodex,
			args:   []string{"--model", "o4-mini", "--search", "--sandbox", "workspace-write"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PrepareManagedLaunchArgs(tc.source, tc.args)
			if err != nil {
				t.Fatalf("PrepareManagedLaunchArgs: %v", err)
			}
			if !reflect.DeepEqual(got, tc.args) {
				t.Fatalf("PrepareManagedLaunchArgs() = %#v, want %#v", got, tc.args)
			}
		})
	}
}

func TestPrepareManagedLaunchArgsRejectsPromptOrSubcommand(t *testing.T) {
	tests := []struct {
		name   string
		source Source
		args   []string
	}{
		{
			name:   "claude prompt",
			source: SourceClaude,
			args:   []string{"--model", "sonnet", "fix the failing test"},
		},
		{
			name:   "codex prompt",
			source: SourceCodex,
			args:   []string{"--model", "o4-mini", "fix the failing test"},
		},
		{
			name:   "codex subcommand",
			source: SourceCodex,
			args:   []string{"resume", "session-123"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PrepareManagedLaunchArgs(tc.source, tc.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "prompts or subcommands") {
				t.Fatalf("expected prompt/subcommand error, got %v", err)
			}
		})
	}
}

func TestPrepareManagedLaunchArgsRejectsConflictingOptions(t *testing.T) {
	tests := []struct {
		name   string
		source Source
		args   []string
		needle string
	}{
		{
			name:   "claude resume",
			source: SourceClaude,
			args:   []string{"--resume", "abc123"},
			needle: "Peek manages session resumption itself",
		},
		{
			name:   "codex cd",
			source: SourceCodex,
			args:   []string{"--cd", "/tmp/project"},
			needle: "active Peek worktree",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PrepareManagedLaunchArgs(tc.source, tc.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.needle) {
				t.Fatalf("expected %q in error, got %v", tc.needle, err)
			}
		})
	}
}
