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

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	"github.com/bskyn/peek/internal/connector/claude"
	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/renderer"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/tailer"
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

	// Set up renderer
	rend := renderer.NewTerminalAuto()
	rend.Source = "Claude"

	// Always use follow mode — automatically switches to new sessions
	return followSessions(ctx, st, rend, claudeDir, sf, replay)
}

// followSessions tails the current session and automatically switches to new sessions.
func followSessions(ctx context.Context, st *store.Store, rend *renderer.TerminalRenderer, claudeDir string, sf *claude.SessionFile, replay bool) error {
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
			tailDone <- tailSession(tailCtx, st, rend, sf, replay)
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
func tailSession(ctx context.Context, st *store.Store, rend *renderer.TerminalRenderer, sf *claude.SessionFile, replay bool) error {
	// Create or look up our internal session
	internalSessionID := "claude-" + sf.SessionID
	_, err := st.GetSession(internalSessionID)
	if err != nil {
		sess := newSessionFromFile(sf, internalSessionID)
		if err := st.CreateSession(sess); err != nil {
			return fmt.Errorf("create session: %w", err)
		}
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

			allInserted := true
			for _, ev := range parsedEvents {
				if err := st.InsertEvent(ev); err != nil {
					fmt.Fprintf(os.Stderr, "store error: %v\n", err)
					allInserted = false
				}
				rend.RenderEvent(ev)
			}

			if !allInserted {
				insertFailed.Store(true)
			}

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
