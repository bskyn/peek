package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	claudeconn "github.com/bskyn/peek/internal/connector/claude"
	codexconn "github.com/bskyn/peek/internal/connector/codex"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/viewer"
)

type loadResult struct {
	sessions int
	events   int
	deleted  int64
}

func newSessionsLoadCmd() *cobra.Command {
	var loadAll bool

	cmd := &cobra.Command{
		Use:   "load",
		Short: "Load sessions from disk into the store",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if !loadAll {
				return fmt.Errorf("--all is required")
			}
			return runSessionsLoadAll()
		},
	}

	cmd.Flags().BoolVar(&loadAll, "all", false, "Reload all Claude and Codex sessions from disk")
	return cmd
}

func newClaudeLoadCmd() *cobra.Command {
	var loadAll bool

	cmd := &cobra.Command{
		Use:   "load",
		Short: "Load Claude sessions from disk into the store",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if !loadAll {
				return fmt.Errorf("--all is required")
			}
			return runClaudeLoadAll()
		},
	}

	cmd.Flags().BoolVar(&loadAll, "all", false, "Reload all Claude sessions from disk")
	return cmd
}

func newCodexLoadCmd() *cobra.Command {
	var loadAll bool

	cmd := &cobra.Command{
		Use:   "load",
		Short: "Load Codex sessions from disk into the store",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if !loadAll {
				return fmt.Errorf("--all is required")
			}
			return runCodexLoadAll()
		},
	}

	cmd.Flags().BoolVar(&loadAll, "all", false, "Reload all Codex sessions from disk")
	return cmd
}

func runSessionsLoadAll() error {
	claudeDir := homeDir() + "/.claude"
	codexDir := codexconn.CodexHome()

	claudeFiles, err := discoverAllClaudeSessions(claudeDir)
	if err != nil {
		return fmt.Errorf("discover Claude sessions: %w", err)
	}
	codexFiles, err := discoverAllCodexSessions(codexDir)
	if err != nil {
		return fmt.Errorf("discover Codex sessions: %w", err)
	}
	if len(claudeFiles) == 0 && len(codexFiles) == 0 {
		return fmt.Errorf("no Claude or Codex sessions found on disk")
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	deleted, err := st.DeleteAllSessions()
	if err != nil {
		return fmt.Errorf("delete stored sessions: %w", err)
	}

	claudeResult, err := loadClaudeSessions(st, nil, claudeFiles)
	if err != nil {
		return fmt.Errorf("load Claude sessions: %w", err)
	}
	codexResult, err := loadCodexSessions(st, nil, codexFiles)
	if err != nil {
		return fmt.Errorf("load Codex sessions: %w", err)
	}

	fmt.Printf(
		"Reloaded %d session(s) from disk (%d deleted, %d Claude, %d Codex, %d events).\n",
		claudeResult.sessions+codexResult.sessions,
		deleted,
		claudeResult.sessions,
		codexResult.sessions,
		claudeResult.events+codexResult.events,
	)
	return nil
}

func runClaudeLoadAll() error {
	claudeDir := homeDir() + "/.claude"
	files, err := claudeconn.DiscoverAll(claudeDir)
	if err != nil {
		return fmt.Errorf("discover Claude sessions: %w", err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	deleted, err := st.DeleteSessionsBySource("claude")
	if err != nil {
		return fmt.Errorf("delete stored Claude sessions: %w", err)
	}

	result, err := loadClaudeSessions(st, nil, files)
	if err != nil {
		return fmt.Errorf("load Claude sessions: %w", err)
	}
	result.deleted = deleted

	fmt.Printf(
		"Reloaded %d Claude session(s) from disk (%d deleted, %d events).\n",
		result.sessions,
		result.deleted,
		result.events,
	)
	return nil
}

func runCodexLoadAll() error {
	codexDir := codexconn.CodexHome()
	files, err := codexconn.DiscoverAll(codexDir)
	if err != nil {
		return fmt.Errorf("discover Codex sessions: %w", err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	deleted, err := st.DeleteSessionsBySource("codex")
	if err != nil {
		return fmt.Errorf("delete stored Codex sessions: %w", err)
	}

	result, err := loadCodexSessions(st, nil, files)
	if err != nil {
		return fmt.Errorf("load Codex sessions: %w", err)
	}
	result.deleted = deleted

	fmt.Printf(
		"Reloaded %d Codex session(s) from disk (%d deleted, %d events).\n",
		result.sessions,
		result.deleted,
		result.events,
	)
	return nil
}

func discoverAllClaudeSessions(claudeDir string) ([]claudeconn.SessionFile, error) {
	projectsDir := filepath.Join(claudeDir, "projects")
	if _, err := os.Stat(projectsDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	files, err := claudeconn.DiscoverAll(claudeDir)
	if err != nil {
		if strings.Contains(err.Error(), "no Claude sessions found") {
			return nil, nil
		}
		return nil, err
	}
	return files, nil
}

func discoverAllCodexSessions(codexDir string) ([]codexconn.SessionFile, error) {
	sessionsDir := filepath.Join(codexDir, "sessions")
	if _, err := os.Stat(sessionsDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	files, err := codexconn.DiscoverAll(codexDir)
	if err != nil {
		if strings.Contains(err.Error(), "no Codex sessions found") {
			return nil, nil
		}
		return nil, err
	}
	return files, nil
}

func loadClaudeSessions(st *store.Store, rt *viewer.Runtime, files []claudeconn.SessionFile) (loadResult, error) {
	result := loadResult{}
	for _, sf := range files {
		internalSessionID := "claude-" + sf.SessionID
		if err := st.CreateSession(sf.ToSession(internalSessionID)); err != nil {
			return result, err
		}
		inserted, err := importClaudeSessionFile(st, rt, &sf, true)
		if err != nil {
			return result, err
		}
		publishSessionSummary(st, rt, internalSessionID)
		result.sessions++
		result.events += inserted
	}
	return result, nil
}

func loadCodexSessions(st *store.Store, rt *viewer.Runtime, files []codexconn.SessionFile) (loadResult, error) {
	result := loadResult{}
	for _, sf := range files {
		internalSessionID := "codex-" + sf.SessionID
		if err := st.CreateSession(sf.ToSession(internalSessionID)); err != nil {
			return result, err
		}
		inserted, err := importCodexSessionFile(st, rt, &sf, true)
		if err != nil {
			return result, err
		}
		publishSessionSummary(st, rt, internalSessionID)
		result.sessions++
		result.events += inserted
	}
	return result, nil
}

func importCodexSessionFile(st *store.Store, rt *viewer.Runtime, sf *codexconn.SessionFile, replay bool) (int, error) {
	file, err := os.Open(sf.Path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	internalSessionID := "codex-" + sf.SessionID
	var seq int64
	annotator := newUsageAnnotator(st, internalSessionID, replay)
	insertedCount := 0
	for scanner.Scan() {
		parsedEvents, nextSeq, err := codexconn.ParseLine(scanner.Text(), internalSessionID, seq)
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

	if err := scanner.Err(); err != nil {
		return insertedCount, err
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
