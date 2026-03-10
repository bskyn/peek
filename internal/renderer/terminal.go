package renderer

import (
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/bskyn/peek/internal/event"
)

// ANSI color codes.
const (
	reset   = "\033[0m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	italic  = "\033[3m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
	gray    = "\033[90m"

	// GitHub-style diff backgrounds (256-color)
	diffRedBg   = "\033[48;5;52m\033[38;5;210m" // dark red bg + light red fg
	diffGreenBg = "\033[48;5;22m\033[38;5;114m" // dark green bg + light green fg
)

const maxOutputLines = 15

// TerminalRenderer renders events to a terminal.
type TerminalRenderer struct {
	w          io.Writer
	color      bool
	seqNum     int64
	Source     string // "Claude", "Codex", etc. — used for assistant message labels
	lastModel  string // most recently seen model, used as fallback
	totalUsage event.Usage
}

// NewTerminal creates a new terminal renderer.
func NewTerminal(w io.Writer, color bool) *TerminalRenderer {
	return &TerminalRenderer{w: w, color: color}
}

// NewTerminalAuto creates a terminal renderer with auto-detected color support.
func NewTerminalAuto() *TerminalRenderer {
	isTerminal := false
	fi, err := os.Stdout.Stat()
	if err == nil {
		isTerminal = fi.Mode()&os.ModeCharDevice != 0
	}
	return NewTerminal(os.Stdout, isTerminal)
}

// RenderEvent renders a single event to the terminal.
func (r *TerminalRenderer) RenderEvent(ev event.Event) {
	r.seqNum++
	r.accumulateUsage(ev.PayloadJSON)
	ts := ev.Timestamp.Format("15:04:05")

	switch ev.Type {
	case event.EventUserMessage:
		text := event.PayloadText(ev.PayloadJSON)
		r.printHeader(ts, r.headerLabel("User", ev.PayloadJSON), blue+bold)
		r.printBodyStyled(text, blue)

	case event.EventAssistantThinking:
		thinking, tokenCount := event.PayloadThinking(ev.PayloadJSON)
		if m := event.PayloadModel(ev.PayloadJSON); m != "" {
			r.lastModel = m
		}
		label := fmt.Sprintf("Thinking (%d tokens)", tokenCount)
		r.printHeader(ts, r.headerLabel(label, ev.PayloadJSON), dim+italic)
		r.printBodyDim(thinking)

	case event.EventAssistantMessage:
		text := event.PayloadText(ev.PayloadJSON)
		if m := event.PayloadModel(ev.PayloadJSON); m != "" {
			r.lastModel = m
		}
		label := r.assistantLabel()
		r.printHeader(ts, r.headerLabel(label, ev.PayloadJSON), magenta+bold)
		r.printBodyStyled(text, magenta)

	case event.EventToolCall:
		if patch := event.PayloadPatchCall(ev.PayloadJSON); patch != nil {
			r.printHeader(ts, r.headerLabel(patchLabel(patch), ev.PayloadJSON), cyan)
			r.printPatchBody(patch)
		} else if edit := event.PayloadEditCall(ev.PayloadJSON); edit != nil {
			r.printHeader(ts, r.headerLabel("Edit: "+edit.FilePath, ev.PayloadJSON), cyan)
			r.printDiff(edit.OldString, edit.NewString)
		} else if write := event.PayloadWriteCall(ev.PayloadJSON); write != nil {
			r.printHeader(ts, r.headerLabel("Write: "+write.FilePath, ev.PayloadJSON), cyan)
			r.printWriteBody(write.Content)
		} else {
			name, input := event.PayloadToolCall(ev.PayloadJSON)
			r.printHeader(ts, r.headerLabel("Tool: "+name, ev.PayloadJSON), cyan)
			r.printBody("> " + input)
		}

	case event.EventToolResult:
		text := event.PayloadText(ev.PayloadJSON)
		if text == "" {
			// Try to get content directly
			text = "(no output)"
		}
		r.printHeader(ts, r.headerLabel("Result", ev.PayloadJSON), green)
		r.printBodyTruncated(text, maxOutputLines)

	case event.EventProgress:
		label := "Progress"
		if event.PayloadProgressSubtype(ev.PayloadJSON) == "token_count" {
			label = "Usage"
		}
		r.printHeader(ts, r.headerLabel(label, ev.PayloadJSON), gray)
		if text := event.PayloadText(ev.PayloadJSON); text != "" {
			r.printBodyTruncated(text, maxOutputLines)
		}

	case event.EventSystem:
		if m := event.PayloadModel(ev.PayloadJSON); m != "" {
			r.lastModel = m
		}
		r.printHeader(ts, r.headerLabel("System", ev.PayloadJSON), gray)

	case event.EventError:
		text := event.PayloadText(ev.PayloadJSON)
		r.printHeader(ts, r.headerLabel("Error", ev.PayloadJSON), red+bold)
		r.printBody(text)
	}

	fmt.Fprintln(r.w)
}

func patchLabel(patch *event.PatchInput) string {
	switch patch.Operation {
	case "add":
		return "Write: " + patch.FilePath
	case "delete":
		return "Delete: " + patch.FilePath
	default:
		if patch.MoveTo != "" {
			return "Edit: " + patch.FilePath + " -> " + patch.MoveTo
		}
		return "Edit: " + patch.FilePath
	}
}

// RenderSessionBanner prints a colored header showing which session is being tailed.
func (r *TerminalRenderer) RenderSessionBanner(sessionID, filePath, projectPath string) {
	if r.color {
		fmt.Fprintf(r.w, "  %s%sTailing session %s%s\n", bold, yellow, sessionID, reset)
		fmt.Fprintf(r.w, "  %sFile: %s%s\n", dim, filePath, reset)
		fmt.Fprintf(r.w, "  %sProject: %s%s\n\n", dim, projectPath, reset)
	} else {
		fmt.Fprintf(r.w, "  Tailing session %s\n", sessionID)
		fmt.Fprintf(r.w, "  File: %s\n", filePath)
		fmt.Fprintf(r.w, "  Project: %s\n\n", projectPath)
	}
}

// RenderNewSessionDivider prints a visible separator when switching to a new session.
func (r *TerminalRenderer) RenderNewSessionDivider() {
	divider := strings.Repeat("─", 60)
	if r.color {
		fmt.Fprintf(r.w, "\n  %s%s%s\n", yellow, divider, reset)
		fmt.Fprintf(r.w, "  %s%s  New session started%s\n", bold, yellow, reset)
		fmt.Fprintf(r.w, "  %s%s%s\n", yellow, divider, reset)
	} else {
		fmt.Fprintf(r.w, "\n  %s\n", divider)
		fmt.Fprintf(r.w, "    New session started\n")
		fmt.Fprintf(r.w, "  %s\n", divider)
	}
}

// assistantLabel returns the source label with a formatted model suffix if available.
// e.g. "Claude (Opus 4.6)" or "Codex (o3-pro)"
func (r *TerminalRenderer) assistantLabel() string {
	label := r.Source
	if label == "" {
		label = "Assistant"
	}
	if r.lastModel != "" {
		label += " (" + formatModel(r.lastModel) + ")"
	}
	return label
}

// modelPattern matches Claude model IDs like "claude-opus-4-6", "claude-sonnet-4-6-20260301".
var modelPattern = regexp.MustCompile(`^claude-(\w+)-(\d+)-(\d+)`)

// formatModel turns a raw model ID into a friendly display name.
// "claude-opus-4-6" → "Opus 4.6", "gpt-5" → "gpt-5" (passthrough for unknown).
func formatModel(model string) string {
	if m := modelPattern.FindStringSubmatch(model); m != nil {
		family := strings.ToUpper(m[1][:1]) + m[1][1:] // capitalize
		return fmt.Sprintf("%s %s.%s", family, m[2], m[3])
	}
	return model
}

func (r *TerminalRenderer) printHeader(ts string, label string, style string) {
	if r.color {
		fmt.Fprintf(r.w, "  %s[%d]%s  %s%s  %s%s%s\n", gray, r.seqNum, reset, gray, ts, style, label, reset)
	} else {
		fmt.Fprintf(r.w, "  [%d]  %s  %s\n", r.seqNum, ts, label)
	}
}

func (r *TerminalRenderer) headerLabel(base string, payload []byte) string {
	usage, ok := event.PayloadUsage(payload)
	if !ok {
		return base
	}

	parts := []string{fmt.Sprintf("token count: %s", formatTokenCount(usage.TotalTokens))}
	if usage.TotalCostUSD > 0 {
		parts = append(parts, fmt.Sprintf("cost %s", formatUSD(usage.TotalCostUSD)))
	}
	return fmt.Sprintf("%s (%s)", base, strings.Join(parts, " | "))
}

func formatTokenCount(value int) string {
	if value <= 0 {
		return "0"
	}
	text := strconv.Itoa(value)
	if len(text) <= 3 {
		return text
	}

	var builder strings.Builder
	prefix := len(text) % 3
	if prefix == 0 {
		prefix = 3
	}
	builder.WriteString(text[:prefix])
	for i := prefix; i < len(text); i += 3 {
		builder.WriteByte(',')
		builder.WriteString(text[i : i+3])
	}
	return builder.String()
}

func formatUSD(value float64) string {
	if value <= 0 {
		return "$0.00"
	}

	abs := math.Abs(value)
	decimals := 2
	switch {
	case abs < 0.001:
		decimals = 6
	case abs < 0.01:
		decimals = 5
	case abs < 0.1:
		decimals = 4
	}

	text := strconv.FormatFloat(value, 'f', decimals, 64)
	text = strings.TrimRight(text, "0")
	text = strings.TrimRight(text, ".")
	if !strings.Contains(text, ".") {
		text += ".00"
	}
	return "$" + text
}

func (r *TerminalRenderer) printBody(text string) {
	if text == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintf(r.w, "     %s\n", line)
	}
}

func (r *TerminalRenderer) printBodyStyled(text string, style string) {
	if text == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		if r.color {
			fmt.Fprintf(r.w, "     %s%s%s\n", style, line, reset)
		} else {
			fmt.Fprintf(r.w, "     %s\n", line)
		}
	}
}

func (r *TerminalRenderer) printBodyDim(text string) {
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if r.color {
			fmt.Fprintf(r.w, "     %s%s%s\n", dim, line, reset)
		} else {
			fmt.Fprintf(r.w, "     %s\n", line)
		}
	}
}

const maxDiffLines = 500

func (r *TerminalRenderer) printDiff(oldStr, newStr string) {
	oldLines := strings.Split(oldStr, "\n")
	newLines := strings.Split(newStr, "\n")

	// Fall back to raw display if diff would be too expensive
	if len(oldLines)+len(newLines) > maxDiffLines {
		r.printDiffFallback(oldLines, newLines)
		return
	}

	// Build a simple line-level diff using longest common subsequence
	lcs := computeLCS(oldLines, newLines)

	var diffLines []diffLine
	oi, ni, li := 0, 0, 0
	for oi < len(oldLines) || ni < len(newLines) {
		if li < len(lcs) && oi < len(oldLines) && oldLines[oi] == lcs[li] &&
			ni < len(newLines) && newLines[ni] == lcs[li] {
			diffLines = append(diffLines, diffLine{kind: ' ', text: lcs[li]})
			oi++
			ni++
			li++
		} else if oi < len(oldLines) && (li >= len(lcs) || oldLines[oi] != lcs[li]) {
			diffLines = append(diffLines, diffLine{kind: '-', text: oldLines[oi]})
			oi++
		} else if ni < len(newLines) && (li >= len(lcs) || newLines[ni] != lcs[li]) {
			diffLines = append(diffLines, diffLine{kind: '+', text: newLines[ni]})
			ni++
		}
	}

	// Render with context: show changed lines and up to 3 context lines around them
	const contextLines = 3
	show := make([]bool, len(diffLines))
	for i, dl := range diffLines {
		if dl.kind != ' ' {
			for j := max(0, i-contextLines); j <= min(len(diffLines)-1, i+contextLines); j++ {
				show[j] = true
			}
		}
	}

	lastShown := -1
	for i, dl := range diffLines {
		if !show[i] {
			continue
		}
		if lastShown >= 0 && i-lastShown > 1 {
			if r.color {
				fmt.Fprintf(r.w, "     %s...%s\n", dim, reset)
			} else {
				fmt.Fprintln(r.w, "     ...")
			}
		}
		lastShown = i

		prefix := " "
		style := ""
		resetStyle := ""
		switch dl.kind {
		case '-':
			prefix = "-"
			if r.color {
				style = diffRedBg
				resetStyle = reset
			}
		case '+':
			prefix = "+"
			if r.color {
				style = diffGreenBg
				resetStyle = reset
			}
		}
		fmt.Fprintf(r.w, "     %s%s %s%s\n", style, prefix, dl.text, resetStyle)
	}
}

func (r *TerminalRenderer) printWriteBody(content string) {
	for _, line := range strings.Split(content, "\n") {
		if r.color {
			fmt.Fprintf(r.w, "     %s+ %s%s\n", diffGreenBg, line, reset)
		} else {
			fmt.Fprintf(r.w, "     + %s\n", line)
		}
	}
}

func (r *TerminalRenderer) printPatchBody(patch *event.PatchInput) {
	if patch.Diff == "" {
		if patch.Operation == "delete" {
			r.printBody("(file deleted)")
		}
		return
	}

	for _, line := range strings.Split(patch.Diff, "\n") {
		style := ""
		resetStyle := ""

		switch {
		case strings.HasPrefix(line, "+"):
			if r.color {
				style = diffGreenBg
				resetStyle = reset
			}
		case strings.HasPrefix(line, "-"):
			if r.color {
				style = diffRedBg
				resetStyle = reset
			}
		case strings.HasPrefix(line, "@@"), strings.HasPrefix(line, "\\"):
			if r.color {
				style = dim
				resetStyle = reset
			}
		}

		fmt.Fprintf(r.w, "     %s%s%s\n", style, line, resetStyle)
	}
}

func (r *TerminalRenderer) printDiffFallback(oldLines, newLines []string) {
	if r.color {
		fmt.Fprintf(r.w, "     %s(diff too large — %d lines, showing raw)%s\n", dim, len(oldLines)+len(newLines), reset)
	} else {
		fmt.Fprintf(r.w, "     (diff too large — %d lines, showing raw)\n", len(oldLines)+len(newLines))
	}
	for _, line := range oldLines {
		if r.color {
			fmt.Fprintf(r.w, "     %s- %s%s\n", diffRedBg, line, reset)
		} else {
			fmt.Fprintf(r.w, "     - %s\n", line)
		}
	}
	for _, line := range newLines {
		if r.color {
			fmt.Fprintf(r.w, "     %s+ %s%s\n", diffGreenBg, line, reset)
		} else {
			fmt.Fprintf(r.w, "     + %s\n", line)
		}
	}
}

type diffLine struct {
	kind byte // ' ', '+', '-'
	text string
}

// computeLCS returns the longest common subsequence of two string slices.
func computeLCS(a, b []string) []string {
	m, n := len(a), len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	result := make([]string, 0, dp[m][n])
	i, j := m, n
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			result = append(result, a[i-1])
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			i--
		} else {
			j--
		}
	}
	// Reverse
	for l, r := 0, len(result)-1; l < r; l, r = l+1, r-1 {
		result[l], result[r] = result[r], result[l]
	}
	return result
}

func (r *TerminalRenderer) accumulateUsage(payload []byte) {
	usage, ok := event.PayloadUsage(payload)
	if !ok {
		return
	}
	r.totalUsage.InputTokens += usage.InputTokens
	r.totalUsage.OutputTokens += usage.OutputTokens
	r.totalUsage.TotalTokens += usage.TotalTokens
	r.totalUsage.InputCostUSD += usage.InputCostUSD
	r.totalUsage.OutputCostUSD += usage.OutputCostUSD
	r.totalUsage.TotalCostUSD += usage.TotalCostUSD
}

// RenderUsageSummary prints the accumulated cost summary.
func (r *TerminalRenderer) RenderUsageSummary() {
	u := r.totalUsage
	if !u.HasTokens() && u.TotalCostUSD == 0 {
		return
	}

	divider := strings.Repeat("─", 40)
	if r.color {
		fmt.Fprintf(r.w, "\n  %s%s%s\n", dim, divider, reset)
		fmt.Fprintf(r.w, "  %sSession Cost Summary%s\n", bold+yellow, reset)
		fmt.Fprintf(r.w, "  %s%s%s\n", dim, divider, reset)
	} else {
		fmt.Fprintf(r.w, "\n  %s\n  Session Cost Summary\n  %s\n", divider, divider)
	}

	fmt.Fprintln(r.w)
	r.printSummaryLine("Total cost", formatUSD(u.TotalCostUSD), bold+green)
	r.printSummaryLine("Input tokens", formatTokenCount(u.InputTokens), "")
	r.printSummaryLine("Output tokens", formatTokenCount(u.OutputTokens), "")
	r.printSummaryLine("Total tokens", formatTokenCount(u.TotalTokens), "")
	fmt.Fprintln(r.w)
}

func (r *TerminalRenderer) printSummaryLine(label, value, style string) {
	if r.color && style != "" {
		fmt.Fprintf(r.w, "  %s%-16s%s %s%s%s\n", dim, label, reset, style, value, reset)
	} else if r.color {
		fmt.Fprintf(r.w, "  %s%-16s%s %s\n", dim, label, reset, value)
	} else {
		fmt.Fprintf(r.w, "  %-16s %s\n", label, value)
	}
}

func (r *TerminalRenderer) printBodyTruncated(text string, maxLines int) {
	lines := strings.Split(text, "\n")
	if len(lines) <= maxLines {
		r.printBody(text)
		return
	}
	for _, line := range lines[:maxLines] {
		fmt.Fprintf(r.w, "     %s\n", line)
	}
	remaining := len(lines) - maxLines
	if r.color {
		fmt.Fprintf(r.w, "     %s... %d more lines%s\n", dim, remaining, reset)
	} else {
		fmt.Fprintf(r.w, "     ... %d more lines\n", remaining)
	}
}
