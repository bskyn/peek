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

	"github.com/bskyn/peek/internal/connector/codex"
	"github.com/bskyn/peek/internal/renderer"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/tailer"
	"github.com/bskyn/peek/internal/viewer"
)

var codexWatchPollInterval = 2 * time.Second

func newCodexCmd() *cobra.Command {
	var replay bool

	cmd := &cobra.Command{
		Use:   "codex [session-id]",
		Short: "Tail a Codex CLI session",
		Long:  "Tail a Codex CLI session in real-time. If no session ID is given, auto-discovers the latest active session and follows new sessions automatically.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var sessionID string
			if len(args) > 0 {
				sessionID = args[0]
			}
			return runCodex(sessionID, replay)
		},
	}

	cmd.Flags().BoolVar(&replay, "replay", false, "Replay the full session from the beginning, ignoring saved cursor")
	addViewerFlags(cmd)

	return cmd
}

func runCodex(sessionID string, replay bool) error {
	codexDir := codex.CodexHome()

	sf, err := codex.Discover(codexDir, sessionID)
	if err != nil {
		return fmt.Errorf("discover session: %w", err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nStopping...")
		cancel()
	}()

	rt, err := viewer.Start(ctx, st, buildViewerOptions("codex-"+sf.SessionID), nil)
	if err != nil {
		return fmt.Errorf("start viewer: %w", err)
	}
	if rt != nil {
		fmt.Printf("Viewer: %s\n\n", rt.InitialURL(buildViewerOptions("codex-"+sf.SessionID)))
	}

	rend := renderer.NewTerminalAuto()
	rend.Source = "Codex"

	// Disable follow-mode when a specific session ID was provided
	if sessionID != "" {
		rend.RenderSessionBanner(sf.SessionID, sf.Path, sf.ProjectPath)
		err = tailCodexSession(ctx, st, rend, rt, sf, replay)
	} else {
		err = followCodexSessions(ctx, st, rend, rt, codexDir, sf, replay)
	}
	rend.RenderUsageSummary()
	return err
}

func followCodexSessions(ctx context.Context, st *store.Store, rend *renderer.TerminalRenderer, rt *viewer.Runtime, codexDir string, sf *codex.SessionFile, replay bool) error {
	for {
		rend.RenderSessionBanner(sf.SessionID, sf.Path, sf.ProjectPath)

		tailCtx, tailCancel := context.WithCancel(ctx)

		newSessionCh := make(chan *codex.SessionFile, 1)
		go watchForNewCodexSession(tailCtx, codexDir, sf, newSessionCh)

		tailDone := make(chan error, 1)
		go func() {
			tailDone <- tailCodexSession(tailCtx, st, rend, rt, sf, replay)
		}()

		var newSF *codex.SessionFile
		select {
		case <-ctx.Done():
			tailCancel()
			<-tailDone
			return nil
		case newSF = <-newSessionCh:
			tailCancel()
			<-tailDone
		case err := <-tailDone:
			tailCancel()
			return err
		}

		rend.RenderNewSessionDivider()
		sf = newSF
		replay = false
	}
}

func tailCodexSession(ctx context.Context, st *store.Store, rend *renderer.TerminalRenderer, rt *viewer.Runtime, sf *codex.SessionFile, replay bool) error {
	internalSessionID := "codex-" + sf.SessionID
	if rt != nil {
		rt.SetActiveSessionID(internalSessionID)
	}

	sess := sf.ToSession(internalSessionID)
	if err := st.CreateSession(sess); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	publishSessionSummary(st, rt, internalSessionID)

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

	tl := tailer.New(sf.Path)

	var seq int64
	if !replay {
		maxSeq, err := st.MaxSeq(internalSessionID)
		if err == nil && maxSeq >= 0 {
			seq = maxSeq + 1
		}
	}
	annotator := newUsageAnnotator(st, internalSessionID, replay)

	var wg sync.WaitGroup
	var insertFailed atomic.Bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		for line := range tl.Lines() {
			parsedEvents, nextSeq, err := codex.ParseLine(line, internalSessionID, seq)
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

	finalOffset, err := tl.Tail(ctx, offset)
	wg.Wait()

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

// watchForNewCodexSession watches for new rollout files scoped to the same project CWD.
// Only watches today's date directory (and new date dirs as they appear) to avoid
// walking the entire historical sessions tree.
func watchForNewCodexSession(ctx context.Context, codexDir string, currentSF *codex.SessionFile, ch chan<- *codex.SessionFile) {
	sessionsDir := filepath.Join(codexDir, "sessions")

	// Snapshot existing rollout files in today's date dir
	todayDir := todayDateDir(sessionsDir)
	knownFiles := snapshotRolloutFiles(todayDir)
	pendingFiles := make(map[string]bool)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer watcher.Close()

	// Watch sessions root (for new date dirs) and today's dir
	_ = watcher.Add(sessionsDir)
	if todayDir != "" {
		addDateDirTree(watcher, todayDir)
	}

	trySwitch := func(path string) bool {
		base := filepath.Base(path)
		if !strings.HasPrefix(base, "rollout-") || !strings.HasSuffix(base, ".jsonl") {
			return false
		}
		if knownFiles[path] {
			return false
		}

		// Codex can create the rollout file before session_meta is flushed.
		// Keep the file pending until we can read the cwd and decide.
		newCWD := codex.ReadCWDFromMeta(path)
		if newCWD == "" {
			pendingFiles[path] = true
			return false
		}

		delete(pendingFiles, path)
		if currentSF.ProjectPath != "" && newCWD != currentSF.ProjectPath {
			knownFiles[path] = true
			return false
		}

		info, err := os.Stat(path)
		if err != nil {
			return false
		}

		knownFiles[path] = true
		sf := &codex.SessionFile{
			Path:        path,
			SessionID:   codex.ExtractUUID(base),
			ProjectPath: newCWD,
			ModTime:     info.ModTime(),
		}
		select {
		case ch <- sf:
		default:
		}
		return true
	}

	ticker := time.NewTicker(codexWatchPollInterval)
	defer ticker.Stop()

	check := func() {
		// Re-evaluate today's dir in case the date rolled over
		dir := todayDateDir(sessionsDir)
		if dir == "" {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			path := filepath.Join(dir, name)
			if knownFiles[path] {
				continue
			}
			if trySwitch(path) {
				return
			}
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
				// New date directory — watch it
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					addDateDirTree(watcher, ev.Name)
				} else {
					if trySwitch(ev.Name) {
						return
					}
				}
				check()
			}
			if ev.Has(fsnotify.Write) && pendingFiles[ev.Name] {
				if trySwitch(ev.Name) {
					return
				}
			}
		case <-watcher.Errors:
			continue
		case <-ticker.C:
			check()
		}
	}
}

// todayDateDir returns the path to today's YYYY/MM/DD directory under sessionsDir.
func todayDateDir(sessionsDir string) string {
	now := time.Now()
	dir := filepath.Join(sessionsDir, now.Format("2006"), now.Format("01"), now.Format("02"))
	if _, err := os.Stat(dir); err != nil {
		// Try creating the year/month dirs in case they don't exist yet
		// but don't error — the dir might not exist until Codex creates a session
		return ""
	}
	return dir
}

// addDateDirTree adds a directory and its children to the watcher.
func addDateDirTree(watcher *fsnotify.Watcher, dir string) {
	_ = watcher.Add(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			_ = watcher.Add(filepath.Join(dir, e.Name()))
		}
	}
}

// snapshotRolloutFiles collects all existing rollout-*.jsonl files in a directory.
func snapshotRolloutFiles(dir string) map[string]bool {
	known := make(map[string]bool)
	if dir == "" {
		return known
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return known
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") {
			known[filepath.Join(dir, name)] = true
		}
	}
	return known
}
