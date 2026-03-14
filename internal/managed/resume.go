package managed

import (
	"fmt"
	"strings"

	"github.com/bskyn/peek/internal/event"
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/workspace"
)

// ResumeMode describes how Peek relaunches a managed provider session.
type ResumeMode string

const (
	ResumeModeFresh      ResumeMode = "fresh"
	ResumeModeTranscript ResumeMode = "transcript_seed"
	ResumeModeProvider   ResumeMode = "provider_resume"
)

// BranchAnchorResolution keeps transcript cutoff and code snapshot resolution separate.
type BranchAnchorResolution struct {
	WorkspaceID  string
	SessionID    string
	CutoffSeq    int64
	SnapshotSeq  int64
	SnapshotKind workspace.SnapshotKind
	GitRef       string
}

// TranscriptSeed is the synthesized prompt used for logical resume.
type TranscriptSeed struct {
	CutoffSeq  int64
	EventCount int
	Prompt     string
}

// ResumeSpec describes how to relaunch a workspace in the managed terminal.
type ResumeSpec struct {
	Source          Source
	WorkspaceID     string
	SessionID       string
	WorktreePath    string
	BaseArgs        []string
	Mode            ResumeMode
	SourceSessionID string
	Seed            *TranscriptSeed
}

// CommandArgs returns the provider CLI arguments for this resume spec.
func (r ResumeSpec) CommandArgs() []string {
	args := append([]string(nil), r.BaseArgs...)

	switch r.Mode {
	case ResumeModeProvider:
		switch r.Source {
		case SourceClaude:
			return append(args, "--resume", r.SourceSessionID)
		case SourceCodex:
			return append(args, "resume", r.SourceSessionID)
		}
	case ResumeModeTranscript:
		if r.Seed != nil && strings.TrimSpace(r.Seed.Prompt) != "" {
			return append(args, r.Seed.Prompt)
		}
	}

	return args
}

// BuildInitialResumeSpec creates the plain initial launch spec for a new managed session.
func BuildInitialResumeSpec(source Source, workspaceID, sessionID, worktreePath string, baseArgs []string) ResumeSpec {
	return ResumeSpec{
		Source:       source,
		WorkspaceID:  workspaceID,
		SessionID:    sessionID,
		WorktreePath: worktreePath,
		BaseArgs:     append([]string(nil), baseArgs...),
		Mode:         ResumeModeFresh,
	}
}

// BuildBranchResumeSpec creates a logical resume spec for a newly branched workspace.
func BuildBranchResumeSpec(st *store.Store, source Source, baseArgs []string, childWorkspaceID, childSessionID, childWorktreePath string, anchor BranchAnchorResolution) (ResumeSpec, error) {
	seed, err := buildTranscriptSeed(st, anchor.SessionID, anchor.CutoffSeq)
	if err != nil {
		return ResumeSpec{}, err
	}

	return ResumeSpec{
		Source:       source,
		WorkspaceID:  childWorkspaceID,
		SessionID:    childSessionID,
		WorktreePath: childWorktreePath,
		BaseArgs:     append([]string(nil), baseArgs...),
		Mode:         resumeModeForSeed(seed),
		Seed:         seed,
	}, nil
}

// BuildSwitchResumeSpec creates the resume spec for switching back to an existing workspace.
func BuildSwitchResumeSpec(st *store.Store, source Source, baseArgs []string, workspaceID, worktreePath string, sess *event.Session) (ResumeSpec, error) {
	if sess == nil {
		return ResumeSpec{}, fmt.Errorf("session is required")
	}

	spec := ResumeSpec{
		Source:          source,
		WorkspaceID:     workspaceID,
		SessionID:       sess.ID,
		WorktreePath:    worktreePath,
		BaseArgs:        append([]string(nil), baseArgs...),
		SourceSessionID: sess.SourceSessionID,
		Mode:            ResumeModeFresh,
	}

	if sess.SourceSessionID != "" {
		spec.Mode = ResumeModeProvider
		return spec, nil
	}

	maxSeq, err := st.MaxSeq(sess.ID)
	if err != nil {
		return ResumeSpec{}, err
	}
	if maxSeq < 0 {
		return spec, nil
	}

	seed, err := buildTranscriptSeed(st, sess.ID, maxSeq)
	if err != nil {
		return ResumeSpec{}, err
	}
	spec.Mode = resumeModeForSeed(seed)
	spec.Seed = seed
	return spec, nil
}

func resumeModeForSeed(seed *TranscriptSeed) ResumeMode {
	if seed == nil || strings.TrimSpace(seed.Prompt) == "" {
		return ResumeModeFresh
	}
	return ResumeModeTranscript
}

func buildTranscriptSeed(st *store.Store, sessionID string, cutoffSeq int64) (*TranscriptSeed, error) {
	events, err := st.GetEvents(sessionID)
	if err != nil {
		return nil, err
	}

	lines := make([]string, 0, len(events))
	for _, ev := range events {
		if ev.Seq > cutoffSeq {
			break
		}
		line := renderSeedLine(ev)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return nil, nil
	}

	var prompt strings.Builder
	prompt.WriteString("Resume this Peek-managed session from the transcript below. Continue naturally from the end in the current worktree.\n\n")
	prompt.WriteString("Transcript:\n")
	for _, line := range lines {
		prompt.WriteString(line)
		prompt.WriteByte('\n')
	}

	return &TranscriptSeed{
		CutoffSeq:  cutoffSeq,
		EventCount: len(lines),
		Prompt:     prompt.String(),
	}, nil
}

func renderSeedLine(ev event.Event) string {
	switch ev.Type {
	case event.EventUserMessage:
		if text := strings.TrimSpace(event.PayloadText(ev.PayloadJSON)); text != "" {
			return "User: " + text
		}
	case event.EventAssistantThinking:
		thinking, _ := event.PayloadThinking(ev.PayloadJSON)
		if thinking != "" {
			return "Assistant thinking: " + thinking
		}
	case event.EventAssistantMessage:
		if text := strings.TrimSpace(event.PayloadText(ev.PayloadJSON)); text != "" {
			return "Assistant: " + text
		}
	case event.EventToolCall:
		name, input := event.PayloadToolCall(ev.PayloadJSON)
		if name == "" {
			return ""
		}
		if input == "" {
			return "Tool call: " + name
		}
		return fmt.Sprintf("Tool call: %s %s", name, input)
	case event.EventToolResult:
		if text := strings.TrimSpace(event.PayloadText(ev.PayloadJSON)); text != "" {
			return "Tool result: " + text
		}
	case event.EventSystem, event.EventError:
		if text := strings.TrimSpace(event.PayloadText(ev.PayloadJSON)); text != "" {
			return "System: " + text
		}
	}
	return ""
}
