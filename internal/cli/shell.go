package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bskyn/peek/internal/store"
)

// workspaceTransitionResult carries the normalized outcome of a branch or switch
// request, including the runtime identity needed for shell attachment.
type workspaceTransitionResult struct {
	RuntimeID    string
	WorkspaceID  string
	SessionID    string
	ProjectPath  string
	WorktreePath string
	Kind         string // "branch" or "switch"
	SourceFrozen string // branch only: the frozen source workspace ID
}

// shellQuote returns a POSIX shell-safe single-quoted string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// renderTransitionShell formats a transition result as eval-safe shell variable
// assignments for consumption by the peek shell hook.
func renderTransitionShell(tr workspaceTransitionResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "__PEEK_RUNTIME_ID=%s\n", shellQuote(tr.RuntimeID))
	fmt.Fprintf(&b, "__PEEK_WORKSPACE_ID=%s\n", shellQuote(tr.WorkspaceID))
	fmt.Fprintf(&b, "__PEEK_SESSION_ID=%s\n", shellQuote(tr.SessionID))
	fmt.Fprintf(&b, "__PEEK_WORKTREE=%s\n", shellQuote(tr.WorktreePath))
	fmt.Fprintf(&b, "__PEEK_PROJECT=%s\n", shellQuote(tr.ProjectPath))
	fmt.Fprintf(&b, "__PEEK_KIND=%s\n", shellQuote(tr.Kind))
	if tr.SourceFrozen != "" {
		fmt.Fprintf(&b, "__PEEK_SOURCE_FROZEN=%s\n", shellQuote(tr.SourceFrozen))
	}
	return b.String()
}

// shellSyncWorktree resolves the active worktree for a bound runtime and returns
// it if the shell should cd. Returns empty string when no action is needed.
func shellSyncWorktree(st *store.Store, runtimeID, followMode, cwd string) string {
	if runtimeID == "" || followMode == "pinned" {
		return ""
	}

	rt, err := st.GetManagedRuntime(runtimeID)
	if err != nil {
		return ""
	}
	if rt.Status != store.ManagedRuntimeRunning || time.Since(rt.HeartbeatAt) > managedRuntimeStaleAfter {
		return ""
	}

	ws, err := st.GetWorkspace(rt.ActiveWorkspaceID)
	if err != nil || ws.WorktreePath == "" {
		return ""
	}

	if cwd == ws.WorktreePath || strings.HasPrefix(cwd, ws.WorktreePath+"/") {
		return ""
	}

	return ws.WorktreePath
}

func newShellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell",
		Short: "Shell integration for runtime-bound workspace follow",
		Long: `Shell integration lets attached shells automatically follow the active
workspace for a managed runtime. Set up once with:

  eval "$(peek shell init zsh)"   # or bash

Then attach to a runtime:

  eval "$(peek shell attach <runtime-id>)"

Attached shells auto-cd on workspace branch/switch and converge at each
prompt via the precmd hook. Pin a shell to hold it in place for review:

  eval "$(peek shell pin)"
  eval "$(peek shell unpin)"`,
	}

	cmd.AddCommand(newShellInitCmd())
	cmd.AddCommand(newShellAttachCmd())
	cmd.AddCommand(newShellDetachCmd())
	cmd.AddCommand(newShellStatusCmd())
	cmd.AddCommand(newShellPinCmd())
	cmd.AddCommand(newShellUnpinCmd())
	cmd.AddCommand(newShellSyncCmd())

	return cmd
}

func newShellInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init <zsh|bash>",
		Short: "Print shell hook code to stdout",
		Long:  "Prints shell integration code. Add to your shell rc file:\n\n  eval \"$(peek shell init zsh)\"",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			switch args[0] {
			case "zsh":
				fmt.Print(zshHookTemplate)
			case "bash":
				fmt.Print(bashHookTemplate)
			default:
				return fmt.Errorf("unsupported shell %q (supported: zsh, bash)", args[0])
			}
			return nil
		},
	}
}

func newShellAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <runtime-id>",
		Short: "Bind this shell to a managed runtime",
		Long:  "Prints eval-safe exports. Usage: eval \"$(peek shell attach <runtime-id>)\"",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			rtID := args[0]

			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			if _, err := st.GetManagedRuntime(rtID); err != nil {
				return fmt.Errorf("runtime %s not found", rtID)
			}

			fmt.Printf("export PEEK_RUNTIME_ID=%s\n", shellQuote(rtID))
			fmt.Println("export PEEK_FOLLOW_MODE='follow'")
			return nil
		},
	}
}

func newShellDetachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detach",
		Short: "Unbind this shell from its managed runtime",
		Long:  "Prints eval-safe unsets. Usage: eval \"$(peek shell detach)\"",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("unset PEEK_RUNTIME_ID")
			fmt.Println("unset PEEK_FOLLOW_MODE")
			return nil
		},
	}
}

func newShellStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show this shell's runtime binding and follow mode",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			rtID := os.Getenv("PEEK_RUNTIME_ID")
			mode := os.Getenv("PEEK_FOLLOW_MODE")
			if mode == "" {
				mode = "follow"
			}

			if rtID == "" {
				fmt.Println("Not attached to any runtime.")
				return nil
			}

			fmt.Printf("Runtime:     %s\n", rtID)
			fmt.Printf("Follow mode: %s\n", mode)

			st, err := store.Open(dbPath)
			if err != nil {
				return nil
			}
			defer st.Close()

			rt, err := st.GetManagedRuntime(rtID)
			if err != nil {
				fmt.Println("Status:      not found")
				return nil
			}

			switch {
			case rt.Status == store.ManagedRuntimeRunning && time.Since(rt.HeartbeatAt) <= managedRuntimeStaleAfter:
				fmt.Println("Status:      running")
			case rt.Status == store.ManagedRuntimeRunning:
				fmt.Println("Status:      stale")
			default:
				fmt.Printf("Status:      %s\n", rt.Status)
			}

			fmt.Printf("Workspace:   %s\n", rt.ActiveWorkspaceID)
			fmt.Printf("Project:     %s\n", rt.ProjectPath)

			if ws, wsErr := st.GetWorkspace(rt.ActiveWorkspaceID); wsErr == nil && ws.WorktreePath != "" {
				fmt.Printf("Worktree:    %s\n", ws.WorktreePath)
			}

			return nil
		},
	}
}

func newShellPinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pin",
		Short: "Pin this shell so it stops auto-following workspace changes",
		Long:  "Prints eval-safe export. Usage: eval \"$(peek shell pin)\"",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if os.Getenv("PEEK_RUNTIME_ID") == "" {
				return fmt.Errorf("not attached to any runtime; run: eval \"$(peek shell attach <runtime-id>)\"")
			}
			fmt.Println("export PEEK_FOLLOW_MODE='pinned'")
			return nil
		},
	}
}

func newShellUnpinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unpin",
		Short: "Resume auto-following the runtime's active workspace",
		Long:  "Prints eval-safe export. Usage: eval \"$(peek shell unpin)\"",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if os.Getenv("PEEK_RUNTIME_ID") == "" {
				return fmt.Errorf("not attached to any runtime; run: eval \"$(peek shell attach <runtime-id>)\"")
			}
			fmt.Println("export PEEK_FOLLOW_MODE='follow'")
			return nil
		},
	}
}

func newShellSyncCmd() *cobra.Command {
	var rtIDFlag string

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Print the worktree path if this shell should cd (used by prompt hook)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			rtID := rtIDFlag
			if rtID == "" {
				rtID = os.Getenv("PEEK_RUNTIME_ID")
			}
			mode := os.Getenv("PEEK_FOLLOW_MODE")
			cwd, err := os.Getwd()
			if err != nil {
				return nil
			}

			st, err := store.Open(dbPath)
			if err != nil {
				return nil
			}
			defer st.Close()

			if target := shellSyncWorktree(st, rtID, mode, cwd); target != "" {
				fmt.Println(target)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&rtIDFlag, "runtime-id", "", "Runtime ID (defaults to PEEK_RUNTIME_ID env)")
	return cmd
}

const zshHookTemplate = `# Peek shell integration — add to .zshrc:
#   eval "$(peek shell init zsh)"

peek() {
  # Pass through --help / -h without hook interception.
  local _a; for _a in "$@"; do
    [[ "$_a" == "--help" || "$_a" == "-h" ]] && { command peek "$@"; return $?; }
  done

  case "$1:$2" in
    workspace:branch|workspace:switch|ws:branch|ws:switch)
      local _peek_vars
      _peek_vars=$(command peek "$@" --shell)
      local _peek_rc=$?
      [[ $_peek_rc -ne 0 ]] && return $_peek_rc
      eval "$_peek_vars"
      [[ -n "$__PEEK_RUNTIME_ID" ]] && export PEEK_RUNTIME_ID="$__PEEK_RUNTIME_ID"
      [[ "$PEEK_FOLLOW_MODE" != "pinned" && -n "$__PEEK_WORKTREE" && -d "$__PEEK_WORKTREE" ]] && cd "$__PEEK_WORKTREE"
      ;;
    shell:attach|shell:detach|shell:pin|shell:unpin)
      local _peek_out
      _peek_out=$(command peek "$@")
      local _peek_rc=$?
      [[ $_peek_rc -ne 0 ]] && return $_peek_rc
      eval "$_peek_out"
      ;;
    *)
      command peek "$@"
      ;;
  esac
}

__peek_precmd() {
  [[ -z "$PEEK_RUNTIME_ID" || "$PEEK_FOLLOW_MODE" == "pinned" ]] && return
  local _peek_target
  _peek_target=$(command peek shell sync 2>/dev/null)
  [[ -n "$_peek_target" && "$_peek_target" != "$PWD" ]] && cd "$_peek_target"
}

autoload -Uz add-zsh-hook
add-zsh-hook precmd __peek_precmd
`

const bashHookTemplate = `# Peek shell integration — add to .bashrc:
#   eval "$(peek shell init bash)"

peek() {
  # Pass through --help / -h without hook interception.
  local _a; for _a in "$@"; do
    [[ "$_a" == "--help" || "$_a" == "-h" ]] && { command peek "$@"; return $?; }
  done

  case "$1:$2" in
    workspace:branch|workspace:switch|ws:branch|ws:switch)
      local _peek_vars
      _peek_vars=$(command peek "$@" --shell)
      local _peek_rc=$?
      [[ $_peek_rc -ne 0 ]] && return $_peek_rc
      eval "$_peek_vars"
      [[ -n "$__PEEK_RUNTIME_ID" ]] && export PEEK_RUNTIME_ID="$__PEEK_RUNTIME_ID"
      [[ "$PEEK_FOLLOW_MODE" != "pinned" && -n "$__PEEK_WORKTREE" && -d "$__PEEK_WORKTREE" ]] && cd "$__PEEK_WORKTREE"
      ;;
    shell:attach|shell:detach|shell:pin|shell:unpin)
      local _peek_out
      _peek_out=$(command peek "$@")
      local _peek_rc=$?
      [[ $_peek_rc -ne 0 ]] && return $_peek_rc
      eval "$_peek_out"
      ;;
    *)
      command peek "$@"
      ;;
  esac
}

__peek_prompt_command() {
  [[ -z "$PEEK_RUNTIME_ID" || "$PEEK_FOLLOW_MODE" == "pinned" ]] && return
  local _peek_target
  _peek_target=$(command peek shell sync 2>/dev/null)
  [[ -n "$_peek_target" && "$_peek_target" != "$PWD" ]] && cd "$_peek_target"
}

PROMPT_COMMAND="__peek_prompt_command${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
`
