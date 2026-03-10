package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bskyn/peek/internal/event"
)

// SessionFile represents a discovered Claude session on disk.
type SessionFile struct {
	Path              string
	SessionID         string
	ProjectPath       string
	EncodedProjectKey string
	ParentSessionID   string
	ModTime           time.Time
}

// ToSession converts a SessionFile to an event.Session.
func (sf *SessionFile) ToSession(internalID string) event.Session {
	now := time.Now().UTC()
	return event.Session{
		ID:              internalID,
		Source:          "claude",
		ProjectPath:     sf.ProjectPath,
		SourceSessionID: sf.SessionID,
		ParentSessionID: sf.ParentSessionID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// Discover finds a Claude session JSONL file. If sessionID is empty, it auto-discovers
// the most recently active session.
func Discover(claudeDir string, sessionID string) (*SessionFile, error) {
	if sessionID != "" {
		return discoverByID(claudeDir, sessionID)
	}
	return discoverLatest(claudeDir)
}

// DiscoverAll finds all Claude session files in deterministic import order.
// Root sessions are returned before their subagents.
func DiscoverAll(claudeDir string) ([]SessionFile, error) {
	projectsDir := filepath.Join(claudeDir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, fmt.Errorf("read projects dir: %w", err)
	}

	rootFiles := make([]SessionFile, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		projectDir := filepath.Join(projectsDir, entry.Name())
		matches, err := filepath.Glob(filepath.Join(projectDir, "*.jsonl"))
		if err != nil {
			continue
		}

		for _, match := range matches {
			base := filepath.Base(match)
			if strings.HasPrefix(base, "agent-") {
				continue
			}

			info, err := os.Stat(match)
			if err != nil {
				continue
			}

			rootFiles = append(rootFiles, SessionFile{
				Path:              match,
				SessionID:         strings.TrimSuffix(base, ".jsonl"),
				ProjectPath:       decodeProjectKey(entry.Name()),
				EncodedProjectKey: entry.Name(),
				ModTime:           info.ModTime(),
			})
		}
	}

	if len(rootFiles) == 0 {
		return nil, fmt.Errorf("no Claude sessions found in %s", projectsDir)
	}

	sortSessionFiles(rootFiles)

	files := make([]SessionFile, 0, len(rootFiles))
	seenChildren := make(map[string]struct{})
	for _, root := range rootFiles {
		files = append(files, root)

		children, err := DiscoverSubagents(claudeDir, &root)
		if err != nil {
			continue
		}
		for _, child := range children {
			if _, ok := seenChildren[child.Path]; ok {
				continue
			}
			seenChildren[child.Path] = struct{}{}
			files = append(files, child)
		}
	}

	return files, nil
}

func discoverByID(claudeDir string, sessionID string) (*SessionFile, error) {
	projectsDir := filepath.Join(claudeDir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, fmt.Errorf("read projects dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(projectsDir, entry.Name(), sessionID+".jsonl")
		info, err := os.Stat(candidate)
		if err == nil {
			return &SessionFile{
				Path:              candidate,
				SessionID:         sessionID,
				EncodedProjectKey: entry.Name(),
				ProjectPath:       decodeProjectKey(entry.Name()),
				ModTime:           info.ModTime(),
			}, nil
		}

		subagentCandidate := filepath.Join(projectsDir, entry.Name(), "subagents", sessionID+".jsonl")
		info, err = os.Stat(subagentCandidate)
		if err != nil {
			continue
		}

		return &SessionFile{
			Path:              subagentCandidate,
			SessionID:         sessionID,
			EncodedProjectKey: entry.Name(),
			ProjectPath:       decodeProjectKey(entry.Name()),
			ParentSessionID:   latestRootSessionID(filepath.Join(projectsDir, entry.Name())),
			ModTime:           info.ModTime(),
		}, nil
	}

	return nil, fmt.Errorf("session %q not found in %s", sessionID, projectsDir)
}

// DiscoverSubagents finds Claude subagent session files for a root session.
func DiscoverSubagents(claudeDir string, parent *SessionFile) ([]SessionFile, error) {
	if parent == nil {
		return nil, fmt.Errorf("parent session is required")
	}

	projectDir := filepath.Join(claudeDir, "projects", parent.EncodedProjectKey)
	patterns := []string{
		filepath.Join(projectDir, "subagents", "agent-*.jsonl"),
		filepath.Join(projectDir, "agent-*.jsonl"),
	}

	files := make([]SessionFile, 0)
	seen := make(map[string]struct{})
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, match := range matches {
			if _, ok := seen[match]; ok {
				continue
			}
			seen[match] = struct{}{}

			info, err := os.Stat(match)
			if err != nil {
				continue
			}

			files = append(files, SessionFile{
				Path:              match,
				SessionID:         strings.TrimSuffix(filepath.Base(match), ".jsonl"),
				ProjectPath:       parent.ProjectPath,
				EncodedProjectKey: parent.EncodedProjectKey,
				ParentSessionID:   parent.SessionID,
				ModTime:           info.ModTime(),
			})
		}
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].ModTime.Equal(files[j].ModTime) {
			return files[i].SessionID < files[j].SessionID
		}
		return files[i].ModTime.Before(files[j].ModTime)
	})

	return files, nil
}

func discoverLatest(claudeDir string) (*SessionFile, error) {
	// Try history.jsonl first for the latest session
	sf, err := discoverFromHistory(claudeDir)
	if err == nil {
		return sf, nil
	}

	// Fallback: scan all project dirs for most recently modified JSONL
	return discoverByMtime(claudeDir)
}

// historyEntry matches a line in ~/.claude/history.jsonl.
type historyEntry struct {
	SessionID string `json:"sessionId"`
	Project   string `json:"project"`
	Timestamp int64  `json:"timestamp"`
}

func discoverFromHistory(claudeDir string) (*SessionFile, error) {
	histPath := filepath.Join(claudeDir, "history.jsonl")
	data, err := os.ReadFile(histPath)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty history file")
	}

	// Find the latest entry (last line with a sessionId)
	for i := len(lines) - 1; i >= 0; i-- {
		var entry historyEntry
		if err := json.Unmarshal([]byte(lines[i]), &entry); err != nil {
			continue
		}
		if entry.SessionID == "" {
			continue
		}

		sf, err := discoverByID(claudeDir, entry.SessionID)
		if err == nil {
			return sf, nil
		}
	}

	return nil, fmt.Errorf("no valid session found in history")
}

// DiscoverByMtime finds the most recently modified session JSONL file.
func DiscoverByMtime(claudeDir string) (*SessionFile, error) {
	return discoverByMtime(claudeDir)
}

func discoverByMtime(claudeDir string) (*SessionFile, error) {
	projectsDir := filepath.Join(claudeDir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, fmt.Errorf("no sessions found: cannot read %s: %w", projectsDir, err)
	}

	var files []SessionFile
	for _, projEntry := range entries {
		if !projEntry.IsDir() {
			continue
		}
		projDir := filepath.Join(projectsDir, projEntry.Name())
		jsonlFiles, err := filepath.Glob(filepath.Join(projDir, "*.jsonl"))
		if err != nil {
			continue
		}
		for _, f := range jsonlFiles {
			base := filepath.Base(f)
			// Skip agent-*.jsonl (subagent files) for auto-discovery
			if strings.HasPrefix(base, "agent-") {
				continue
			}
			info, err := os.Stat(f)
			if err != nil {
				continue
			}
			sessID := strings.TrimSuffix(base, ".jsonl")
			files = append(files, SessionFile{
				Path:              f,
				SessionID:         sessID,
				EncodedProjectKey: projEntry.Name(),
				ProjectPath:       decodeProjectKey(projEntry.Name()),
				ModTime:           info.ModTime(),
			})
		}
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no Claude sessions found in %s", projectsDir)
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].ModTime.Equal(files[j].ModTime) {
			if files[i].SessionID == files[j].SessionID {
				return files[i].Path < files[j].Path
			}
			return files[i].SessionID < files[j].SessionID
		}
		return files[i].ModTime.After(files[j].ModTime)
	})

	return &files[0], nil
}

func sortSessionFiles(files []SessionFile) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].ModTime.Equal(files[j].ModTime) {
			if files[i].SessionID == files[j].SessionID {
				return files[i].Path < files[j].Path
			}
			return files[i].SessionID < files[j].SessionID
		}
		return files[i].ModTime.Before(files[j].ModTime)
	})
}

// decodeProjectKey converts Claude's encoded project folder name back to a path.
// Claude encodes paths by replacing / with - (roughly).
func decodeProjectKey(key string) string {
	// Claude uses a specific encoding: the folder name is the project path
	// with slashes and other chars replaced. This is a best-effort decode.
	// The exact encoding may vary; we store the raw key and the decoded attempt.
	decoded := strings.ReplaceAll(key, "-", "/")
	if !strings.HasPrefix(decoded, "/") {
		decoded = "/" + decoded
	}
	return decoded
}

func latestRootSessionID(projectDir string) string {
	matches, err := filepath.Glob(filepath.Join(projectDir, "*.jsonl"))
	if err != nil {
		return ""
	}

	var latestID string
	var latestTime time.Time
	for _, match := range matches {
		base := filepath.Base(match)
		if strings.HasPrefix(base, "agent-") {
			continue
		}
		info, err := os.Stat(match)
		if err != nil {
			continue
		}
		if latestID == "" || info.ModTime().After(latestTime) {
			latestID = strings.TrimSuffix(base, ".jsonl")
			latestTime = info.ModTime()
		}
	}
	return latestID
}
