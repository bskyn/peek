package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	"github.com/bskyn/peek/internal/connector/claude"
	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/jsonl"
	"github.com/bskyn/peek/internal/renderer"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/tailer"
	"github.com/bskyn/peek/internal/viewer"
)

func newClaudeCmd() *cobra.Command {
	var replay bool

	cmd := &cobra.Command{
		Use:   "claude [session-id]",
		Short: "Tail a Claude Code session",
		Long:  "Tail a Claude Code session in real-time. If no session ID is given, auto-discovers the latest active session and follows new sessions automatically.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var sessionID string
			if len(args) > 0 {
				sessionID = args[0]
			}
			return runClaude(sessionID, replay)
		},
	}

	cmd.Flags().BoolVar(&replay, "replay", false, "Replay the full session from the beginning, ignoring saved cursor")
	addViewerFlags(cmd)
	cmd.AddCommand(newClaudeLoadCmd())

	return cmd
}

func runClaude(sessionID string, replay bool) error {
	claudeDir := homeDir() + "/.claude"

	// Discover session
	sf, err := claude.Discover(claudeDir, sessionID)
	if err != nil {
		return fmt.Errorf("discover session: %w", err)
	}

	// Open store
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	// Set up context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nStopping...")
		cancel()
	}()

	rt, err := viewer.Start(ctx, st, buildViewerOptions("claude-"+sf.SessionID), nil)
	if err != nil {
		return fmt.Errorf("start viewer: %w", err)
	}
	if rt != nil {
		fmt.Printf("Viewer: %s\n\n", rt.InitialURL(buildViewerOptions("claude-"+sf.SessionID)))
	}

	// Set up renderer
	rend := renderer.NewTerminalAuto()
	rend.Source = "Claude"

	// Always use follow mode — automatically switches to new sessions
	err = followSessions(ctx, st, rend, rt, claudeDir, sf, replay)
	rend.RenderUsageSummary()
	return err
}

// followSessions tails the current session and automatically switches to new sessions.
func followSessions(ctx context.Context, st *store.Store, rend *renderer.TerminalRenderer, rt *viewer.Runtime, claudeDir string, sf *claude.SessionFile, replay bool) error {
	for {
		rend.RenderSessionBanner(sf.SessionID, sf.Path, sf.ProjectPath)

		// Create a cancellable child context for this session's tailing
		tailCtx, tailCancel := context.WithCancel(ctx)

		// Channel to receive new session notifications
		newSessionCh := make(chan *claude.SessionFile, 1)

		// Watch for new sessions in the same project directory
		go watchForNewSession(tailCtx, claudeDir, sf, newSessionCh)

		// Tail in background
		tailDone := make(chan error, 1)
		go func() {
			tailDone <- tailSession(tailCtx, st, rend, rt, claudeDir, sf, replay)
		}()

		// Wait for: new session detected, tail error, or parent context done
		var newSF *claude.SessionFile
		select {
		case <-ctx.Done():
			tailCancel()
			<-tailDone
			return nil
		case newSF = <-newSessionCh:
			tailCancel()
			<-tailDone // wait for tailer to finish and save cursor
		case err := <-tailDone:
			tailCancel()
			return err
		}

		// New session detected
		rend.RenderNewSessionDivider()
		sf = newSF
		replay = false // new session always starts from beginning
	}
}

// tailSession tails a single session until the context is cancelled.
func tailSession(ctx context.Context, st *store.Store, rend *renderer.TerminalRenderer, rt *viewer.Runtime, claudeDir string, sf *claude.SessionFile, replay bool) error {
	// Create or look up our internal session
	internalSessionID := "claude-" + sf.SessionID
	if rt != nil {
		rt.SetActiveSessionID(internalSessionID)
	}
	sess := newSessionFromFile(sf, internalSessionID)
	if err := st.CreateSession(sess); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	publishSessionSummary(st, rt, internalSessionID)

	if err := syncClaudeSubagents(st, rt, claudeDir, sf); err != nil {
		return fmt.Errorf("sync subagents: %w", err)
	}

	// Load cursor for resume (unless --replay)
	var offset int64
	if !replay {
		cursor, err := st.GetCursor(sf.Path)
		if err == nil {
			offset = cursor.ByteOffset
			if verbose {
				fmt.Printf("  Resuming from offset %d\n\n", offset)
			}
		}
	}

	// Start tailing
	tl := tailer.New(sf.Path)

	// Load current seq from store (unless replaying)
	var seq int64
	if !replay {
		maxSeq, err := st.MaxSeq(internalSessionID)
		if err == nil && maxSeq >= 0 {
			seq = maxSeq + 1
		}
	}
	annotator := newUsageAnnotator(st, internalSessionID, replay)

	// Process lines in a goroutine
	var wg sync.WaitGroup
	var insertFailed atomic.Bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		for line := range tl.Lines() {
			parsedEvents, nextSeq, err := claude.ParseLine(line, internalSessionID, seq)
			if err != nil {
				if verbose {
					fmt.Fprintf(os.Stderr, "parse error: %v\n", err)
				}
				continue
			}
			parsedEvents = annotator.Annotate(parsedEvents)

			insertedEvents, err := st.AppendEvents(parsedEvents)
			if err != nil {
				fmt.Fprintf(os.Stderr, "store error: %v\n", err)
				insertFailed.Store(true)
				continue
			}

			for _, ev := range parsedEvents {
				rend.RenderEvent(ev)
			}

			publishSessionSummary(st, rt, internalSessionID)
			publishInsertedEvents(rt, insertedEvents)
			seq = nextSeq
		}
	}()

	// Tail blocks until context is cancelled
	finalOffset, err := tl.Tail(ctx, offset)

	// Wait for line processing to complete
	wg.Wait()

	// Only save cursor if all events were persisted successfully
	if insertFailed.Load() {
		fmt.Fprintf(os.Stderr, "warning: some events failed to persist, cursor not advanced\n")
	} else {
		saveCursor := store.Cursor{
			Path:       sf.Path,
			ByteOffset: finalOffset,
			SessionID:  internalSessionID,
		}
		if saveErr := st.SaveCursor(saveCursor); saveErr != nil {
			fmt.Fprintf(os.Stderr, "save cursor error: %v\n", saveErr)
		}
	}

	if verbose {
		fmt.Printf("\nSaved cursor at offset %d\n", finalOffset)
	}

	return err
}

func publishSessionSummary(st *store.Store, rt *viewer.Runtime, sessionID string) {
	if rt == nil {
		return
	}
	summary, err := st.GetSessionSummary(sessionID)
	if err != nil {
		return
	}
	rt.Broker().PublishSessionUpsert(summary)
}

func publishInsertedEvents(rt *viewer.Runtime, events []event.Event) {
	if rt == nil {
		return
	}
	for _, ev := range events {
		rt.Broker().PublishEventAppend(ev)
	}
}

func syncClaudeSubagents(st *store.Store, rt *viewer.Runtime, claudeDir string, parent *claude.SessionFile) error {
	if strings.HasPrefix(parent.SessionID, "agent-") {
		return nil
	}

	children, err := claude.DiscoverSubagents(claudeDir, parent)
	if err != nil {
		return nil
	}

	for _, child := range children {
		internalID := "claude-" + child.SessionID
		if err := st.CreateSession(child.ToSession(internalID)); err != nil {
			return err
		}
		if _, err := importClaudeSessionFile(st, rt, &child, false); err != nil {
			return err
		}
		publishSessionSummary(st, rt, internalID)
	}

	return nil
}

func importClaudeSessionFile(st *store.Store, rt *viewer.Runtime, sf *claude.SessionFile, replay bool) (int, error) {
	file, err := os.Open(sf.Path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)

	internalSessionID := "claude-" + sf.SessionID
	var seq int64
	annotator := newUsageAnnotator(st, internalSessionID, replay)
	insertedCount := 0
	for {
		line, _, _, err := jsonl.ReadLine(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			return insertedCount, err
		}

		parsedEvents, nextSeq, err := claude.ParseLine(string(line), internalSessionID, seq)
		if err != nil {
			continue
		}
		parsedEvents = annotator.Annotate(parsedEvents)
		insertedEvents, err := st.AppendEvents(parsedEvents)
		if err != nil {
			return insertedCount, err
		}
		publishInsertedEvents(rt, insertedEvents)
		insertedCount += len(insertedEvents)
		seq = nextSeq
	}

	info, err := file.Stat()
	if err != nil {
		return insertedCount, err
	}
	if err := st.SaveCursor(store.Cursor{
		Path:       sf.Path,
		ByteOffset: info.Size(),
		SessionID:  internalSessionID,
	}); err != nil {
		return insertedCount, err
	}

	return insertedCount, nil
}

// watchForNewSession watches the same project directory for a new session JSONL file.
// When /clear or /new is used, Claude creates a new file in the same project dir.
func watchForNewSession(ctx context.Context, claudeDir string, currentSF *claude.SessionFile, ch chan<- *claude.SessionFile) {
	projectDir := filepath.Join(claudeDir, "projects", currentSF.EncodedProjectKey)

	// Snapshot existing files so we only detect truly new ones
	knownFiles := make(map[string]bool)
	if entries, err := filepath.Glob(filepath.Join(projectDir, "*.jsonl")); err == nil {
		for _, f := range entries {
			knownFiles[f] = true
		}
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer watcher.Close()

	_ = watcher.Add(projectDir)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	check := func() {
		entries, err := filepath.Glob(filepath.Join(projectDir, "*.jsonl"))
		if err != nil {
			return
		}
		for _, f := range entries {
			if knownFiles[f] {
				continue
			}
			base := filepath.Base(f)
			if strings.HasPrefix(base, "agent-") {
				continue
			}
			sessID := strings.TrimSuffix(base, ".jsonl")
			sf := &claude.SessionFile{
				Path:              f,
				SessionID:         sessID,
				EncodedProjectKey: currentSF.EncodedProjectKey,
				ProjectPath:       currentSF.ProjectPath,
			}
			if info, err := os.Stat(f); err == nil {
				sf.ModTime = info.ModTime()
			}
			select {
			case ch <- sf:
			default:
			}
			return
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if ev.Has(fsnotify.Create) {
				check()
			}
		case <-watcher.Errors:
			continue
		case <-ticker.C:
			check()
		}
	}
}

func newSessionFromFile(sf *claude.SessionFile, internalID string) event.Session {
	return sf.ToSession(internalID)
}
