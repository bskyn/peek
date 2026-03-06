package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bskyn/peek/internal/event"
)

// SessionFile represents a discovered Codex rollout session on disk.
type SessionFile struct {
	Path        string
	SessionID   string // UUID extracted from filename
	ProjectPath string // cwd from session_meta
	ModTime     time.Time
}

// ToSession converts a SessionFile to an event.Session.
func (sf *SessionFile) ToSession(internalID string) event.Session {
	now := time.Now().UTC()
	return event.Session{
		ID:              internalID,
		Source:          "codex",
		ProjectPath:     sf.ProjectPath,
		SourceSessionID: sf.SessionID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// Discover finds a Codex rollout JSONL file. If sessionID is empty, it auto-discovers
// the most recently modified session. Respects CODEX_HOME env var (default ~/.codex).
func Discover(codexDir string, sessionID string) (*SessionFile, error) {
	if sessionID != "" {
		return discoverByID(codexDir, sessionID)
	}
	return discoverLatest(codexDir)
}

func discoverByID(codexDir string, sessionID string) (*SessionFile, error) {
	sessionsDir := filepath.Join(codexDir, "sessions")
	var found *SessionFile

	err := filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if !strings.HasPrefix(base, "rollout-") || !strings.HasSuffix(base, ".jsonl") {
			return nil
		}
		// Match on the extracted UUID suffix, not substring
		if ExtractUUID(base) != sessionID {
			return nil
		}
		sf := &SessionFile{
			Path:      path,
			SessionID: sessionID,
			ModTime:   info.ModTime(),
		}
		sf.ProjectPath = ReadCWDFromMeta(path)
		found = sf
		return filepath.SkipAll
	})
	if err != nil && found == nil {
		return nil, fmt.Errorf("walk sessions dir: %w", err)
	}
	if found == nil {
		return nil, fmt.Errorf("session %q not found in %s", sessionID, sessionsDir)
	}
	return found, nil
}

func discoverLatest(codexDir string) (*SessionFile, error) {
	sessionsDir := filepath.Join(codexDir, "sessions")

	var files []SessionFile
	err := filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if !strings.HasPrefix(base, "rollout-") || !strings.HasSuffix(base, ".jsonl") {
			return nil
		}
		files = append(files, SessionFile{
			Path:    path,
			ModTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk sessions dir: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no Codex sessions found in %s", sessionsDir)
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})

	sf := &files[0]
	sf.SessionID = ExtractUUID(filepath.Base(sf.Path))
	sf.ProjectPath = ReadCWDFromMeta(sf.Path)
	return sf, nil
}

// ExtractUUID pulls the UUID from a rollout filename like:
// rollout-2026-03-05T16-56-31-019cc0a5-6911-7123-b2ff-a4848ccd6e79.jsonl
func ExtractUUID(filename string) string {
	name := strings.TrimPrefix(filename, "rollout-")
	name = strings.TrimSuffix(name, ".jsonl")
	// Format: YYYY-MM-DDTHH-MM-SS-<uuid>
	// The timestamp is 19 chars (e.g. "2026-03-05T16-56-31"), then a dash, then the UUID
	if len(name) > 20 {
		return name[20:]
	}
	return name
}

// ReadCWDFromMeta reads the first line of a rollout file to extract the cwd from session_meta.
// Codex session_meta lines can be 10-15KB+ (includes base_instructions), so we use a large buffer.
func ReadCWDFromMeta(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer
	if !scanner.Scan() {
		return ""
	}
	line := scanner.Text()

	var raw struct {
		Type    string `json:"type"`
		Payload struct {
			CWD string `json:"cwd"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return ""
	}
	if raw.Type != "session_meta" {
		return ""
	}
	return raw.Payload.CWD
}

// CodexHome returns the Codex home directory, respecting CODEX_HOME env var.
func CodexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}
