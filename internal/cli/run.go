package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bskyn/peek/internal/companion"
	"github.com/bskyn/peek/internal/connector/claude"
	"github.com/bskyn/peek/internal/connector/codex"
	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/managed"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/tailer"
	"github.com/bskyn/peek/internal/viewer"
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Launch a managed agent session",
		Long:  "Launch a Peek-managed agent session that keeps the current terminal attached while `peek workspace branch` and `peek workspace switch` control it from another terminal.",
	}

	cmd.AddCommand(newRunClaudeCmd())
	cmd.AddCommand(newRunCodexCmd())

	return cmd
}

func newRunClaudeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claude [flags] [-- extra-args...]",
		Short: "Launch a managed Claude session in the live terminal",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			return runManaged(managed.SourceClaude, args)
		},
	}
	addViewerFlags(cmd)
	cmd.Flags().StringVar(&runtimeID, "runtime-id", "", "Reattach a specific managed runtime by ID")
	return cmd
}

func newRunCodexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "codex [flags] [-- extra-args...]",
		Short: "Launch a managed Codex session in the live terminal",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			return runManaged(managed.SourceCodex, args)
		},
	}
	addViewerFlags(cmd)
	cmd.Flags().StringVar(&runtimeID, "runtime-id", "", "Reattach a specific managed runtime by ID")
	return cmd
}

func runManaged(source managed.Source, extraArgs []string) error {
	launchArgs, err := managed.PrepareManagedLaunchArgs(source, extraArgs)
	if err != nil {
		return err
	}

	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		absProjectDir = projectDir
	}

	runtimeSpec, err := companion.ResolveProjectRuntime(absProjectDir)
	if err != nil {
		return fmt.Errorf("resolve project runtime: %w", err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	// Context + signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		seenInterrupt := false
		for sig := range sigCh {
			switch sig {
			case syscall.SIGINT:
				if !seenInterrupt {
					seenInterrupt = true
					continue
				}
			}
			cancel()
			return
		}
	}()

	orch := managed.NewOrchestrator(st, absProjectDir, managedWorktreeBase())
	start, err := prepareManagedStart(st, orch, source, absProjectDir, runtimeID, launchArgs)
	if err != nil {
		return err
	}

	// Start viewer
	rt, err := viewer.Start(ctx, st, buildViewerOptions(start.activeSessionID, start.runtimeID), nil)
	if err != nil {
		return fmt.Errorf("start viewer: %w", err)
	}
	if rt != nil {
		fmt.Printf("Peek viewer: %s\n", rt.InitialURL(buildViewerOptions(start.activeSessionID, start.runtimeID)))
		fmt.Printf("Peek workspace: %s\n", start.activeWorkspaceID)
		if start.reusedRuntime {
			fmt.Printf("Peek runtime: %s (reattached)\n\n", start.runtimeID)
		} else if start.isolatedRoot {
			fmt.Printf("Peek runtime: %s (isolated root worktree)\n\n", start.runtimeID)
		} else {
			fmt.Printf("Peek runtime: %s\n\n", start.runtimeID)
		}
	}
	if runtimeSpec != nil {
		fmt.Printf("Peek companion runtime: %s\n", runtimeSpec.ConfigSource)
	}
	supervisor := newManagedSupervisor(
		st,
		rt,
		orch,
		source,
		launchArgs,
		start.runtimeID,
		start.rootWorkspaceID,
		start.activeWorkspaceID,
		start.activeSessionID,
		absProjectDir,
		runtimeSpec,
		managedLaunchConfig{},
	)
	defer managed.ResetTerminalEmulatorModes()
	if err := supervisor.Run(ctx); err != nil {
		return err
	}

	fmt.Printf("\nPeek: workspace %s frozen.\n", start.rootWorkspaceID)
	return nil
}

// tailManagedLaunch discovers or resumes the provider session file for one managed launch
// and tails it silently while checkpoints are captured around tool execution.
func tailManagedLaunch(ctx context.Context, st *store.Store, rt *viewer.Runtime, runtimeID string, spec managed.ResumeSpec, ce *managed.CheckpointEngine) {
	switch {
	case spec.SourceSessionID != "":
		tailManagedExistingSession(ctx, st, rt, runtimeID, spec, ce)
	default:
		tailManagedNewSession(ctx, st, rt, runtimeID, spec, ce)
	}
}

func tailManagedNewSession(ctx context.Context, st *store.Store, rt *viewer.Runtime, runtimeID string, spec managed.ResumeSpec, ce *managed.CheckpointEngine) {
	knownFiles := snapshotSessionFiles(spec.Source)
	switch spec.Source {
	case managed.SourceClaude:
		sf := waitForNewClaudeSession(ctx, knownFiles, spec.WorktreePath)
		if sf == nil {
			return
		}
		tailClaudeSilent(ctx, st, rt, runtimeID, spec.SessionID, sf, ce)
	case managed.SourceCodex:
		sf := waitForNewCodexSession(ctx, knownFiles, spec.WorktreePath)
		if sf == nil {
			return
		}
		tailCodexSilent(ctx, st, rt, runtimeID, spec.SessionID, sf, ce)
	}
}

func tailManagedExistingSession(ctx context.Context, st *store.Store, rt *viewer.Runtime, runtimeID string, spec managed.ResumeSpec, ce *managed.CheckpointEngine) {
	switch spec.Source {
	case managed.SourceClaude:
		sf, err := claude.Discover(homeDir()+"/.claude", spec.SourceSessionID)
		if err != nil {
			return
		}
		tailClaudeSilent(ctx, st, rt, runtimeID, spec.SessionID, sf, ce)
	case managed.SourceCodex:
		sf, err := codex.Discover(codex.CodexHome(), spec.SourceSessionID)
		if err != nil {
			return
		}
		tailCodexSilent(ctx, st, rt, runtimeID, spec.SessionID, sf, ce)
	}
}

// waitForNewClaudeSession polls until a new Claude JSONL file appears for the given project.
func waitForNewClaudeSession(ctx context.Context, knownFiles map[string]bool, projectDir string) *claude.SessionFile {
	claudeDir := homeDir() + "/.claude"
	// Claude encodes project paths as folder names. Check if the session file
	// lives under the expected project's directory by matching the encoded key
	// in the file path, since decodeProjectKey is lossy.
	expectedProjectDir := filepath.Join(claudeDir, "projects", claude.EncodeProjectKey(projectDir))

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			sf, err := claude.DiscoverByMtime(claudeDir)
			if err != nil {
				continue
			}
			if knownFiles[sf.Path] {
				continue
			}
			// Filter: session file must be under our project's directory
			if expectedProjectDir != "" && !strings.HasPrefix(sf.Path, expectedProjectDir+"/") {
				continue
			}
			return sf
		}
	}
}

// waitForNewCodexSession polls until a new Codex JSONL file appears for the given project.
func waitForNewCodexSession(ctx context.Context, knownFiles map[string]bool, projectDir string) *codex.SessionFile {
	codexDir := codex.CodexHome()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			sf, err := codex.Discover(codexDir, "")
			if err != nil {
				continue
			}
			if knownFiles[sf.Path] {
				continue
			}
			// Filter by project path if available
			if sf.ProjectPath != "" {
				a, _ := filepath.Abs(sf.ProjectPath)
				b, _ := filepath.Abs(projectDir)
				if a != b {
					continue
				}
			}
			return sf
		}
	}
}

// tailClaudeSilent tails a Claude session file, persisting events to the store + viewer
// without rendering to terminal. Captures checkpoints around tool_call/tool_result events.
func tailClaudeSilent(ctx context.Context, st *store.Store, rt *viewer.Runtime, runtimeID, sessID string, sf *claude.SessionFile, ce *managed.CheckpointEngine) {
	_ = st.CreateSession(event.Session{
		ID:              sessID,
		Source:          "claude",
		ProjectPath:     sf.ProjectPath,
		SourceSessionID: sf.SessionID,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	})

	if rt != nil {
		rt.SetActiveSessionID(runtimeID, sessID)
	}

	tl := tailer.New(sf.Path)
	startOffset, seq := managedTailCursor(st, sf.Path, sessID)
	annotator := newUsageAnnotator(st, sessID, true)

	var wg sync.WaitGroup
	var insertFailed atomic.Bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		for line := range tl.Lines() {
			parsedEvents, nextSeq, err := claude.ParseLine(line, sessID, seq)
			if err != nil {
				continue
			}
			parsedEvents = annotator.Annotate(parsedEvents)

			insertedEvents, err := st.AppendEvents(parsedEvents)
			if err != nil {
				insertFailed.Store(true)
				continue
			}

			// Capture checkpoints around tool execution
			for _, ev := range parsedEvents {
				captureCheckpointForEvent(ce, ev)
			}

			publishRuntimeSessionSummary(st, rt, runtimeID, sessID)
			publishRuntimeInsertedEvents(rt, runtimeID, insertedEvents)
			seq = nextSeq
		}
	}()

	finalOffset, _ := tl.Tail(ctx, startOffset)
	wg.Wait()

	if !insertFailed.Load() {
		_ = st.SaveCursor(store.Cursor{
			Path:       sf.Path,
			ByteOffset: finalOffset,
			SessionID:  sessID,
		})
	}
}

// tailCodexSilent tails a Codex session file silently with checkpoint capture.
func tailCodexSilent(ctx context.Context, st *store.Store, rt *viewer.Runtime, runtimeID, sessID string, sf *codex.SessionFile, ce *managed.CheckpointEngine) {
	_ = st.CreateSession(event.Session{
		ID:              sessID,
		Source:          "codex",
		ProjectPath:     sf.ProjectPath,
		SourceSessionID: sf.SessionID,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	})

	if rt != nil {
		rt.SetActiveSessionID(runtimeID, sessID)
	}

	tl := tailer.New(sf.Path)
	startOffset, seq := managedTailCursor(st, sf.Path, sessID)
	annotator := newUsageAnnotator(st, sessID, true)

	var wg sync.WaitGroup
	var insertFailed atomic.Bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		for line := range tl.Lines() {
			parsedEvents, nextSeq, err := codex.ParseLine(line, sessID, seq)
			if err != nil {
				continue
			}
			parsedEvents = annotator.Annotate(parsedEvents)

			insertedEvents, err := st.AppendEvents(parsedEvents)
			if err != nil {
				insertFailed.Store(true)
				continue
			}

			// Capture checkpoints around tool execution
			for _, ev := range parsedEvents {
				captureCheckpointForEvent(ce, ev)
			}

			publishRuntimeSessionSummary(st, rt, runtimeID, sessID)
			publishRuntimeInsertedEvents(rt, runtimeID, insertedEvents)
			seq = nextSeq
		}
	}()

	finalOffset, _ := tl.Tail(ctx, startOffset)
	wg.Wait()

	if !insertFailed.Load() {
		_ = st.SaveCursor(store.Cursor{
			Path:       sf.Path,
			ByteOffset: finalOffset,
			SessionID:  sessID,
		})
	}
}

func managedTailCursor(st *store.Store, path, sessionID string) (int64, int64) {
	var offset int64
	if cursor, err := st.GetCursor(path); err == nil && cursor.SessionID == sessionID {
		offset = cursor.ByteOffset
	}
	maxSeq, err := st.MaxSeq(sessionID)
	if err != nil || maxSeq < 0 {
		return offset, 0
	}
	return offset, maxSeq + 1
}

// captureCheckpointForEvent captures pre-tool or post-tool checkpoints based on event type.
func captureCheckpointForEvent(ce *managed.CheckpointEngine, ev event.Event) {
	switch ev.Type {
	case event.EventToolCall:
		// Pre-tool: snapshot before the tool modifies code
		_ = ce.CapturePreTool(ev.Seq)
	case event.EventToolResult:
		// Post-tool: snapshot after the tool completed
		_ = ce.CapturePostTool(ev.Seq)
	}
}

// snapshotSessionFiles collects paths of existing session files so we can detect new ones.
func snapshotSessionFiles(source managed.Source) map[string]bool {
	known := make(map[string]bool)

	switch source {
	case managed.SourceClaude:
		claudeDir := homeDir() + "/.claude"
		projectsDir := filepath.Join(claudeDir, "projects")
		entries, err := os.ReadDir(projectsDir)
		if err != nil {
			return known
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			matches, _ := filepath.Glob(filepath.Join(projectsDir, entry.Name(), "*.jsonl"))
			for _, m := range matches {
				known[m] = true
			}
		}

	case managed.SourceCodex:
		codexDir := codex.CodexHome()
		sessionsDir := filepath.Join(codexDir, "sessions")
		now := time.Now()
		todayDir := filepath.Join(sessionsDir, now.Format("2006"), now.Format("01"), now.Format("02"))
		entries, err := os.ReadDir(todayDir)
		if err != nil {
			return known
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.HasPrefix(e.Name(), "rollout-") && strings.HasSuffix(e.Name(), ".jsonl") {
				known[filepath.Join(todayDir, e.Name())] = true
			}
		}
	}

	return known
}
