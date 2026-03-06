package tailer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTailExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644)

	tl := New(path)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var lines []string
	done := make(chan struct{})
	go func() {
		for line := range tl.Lines() {
			lines = append(lines, line)
			if len(lines) == 3 {
				cancel()
			}
		}
		close(done)
	}()

	tl.Tail(ctx, 0)
	<-done

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "line1" || lines[1] != "line2" || lines[2] != "line3" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestTailFromOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644)

	// Offset past "line1\n" (6 bytes)
	tl := New(path)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var lines []string
	done := make(chan struct{})
	go func() {
		for line := range tl.Lines() {
			lines = append(lines, line)
			if len(lines) == 2 {
				cancel()
			}
		}
		close(done)
	}()

	tl.Tail(ctx, 6)
	<-done

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines from offset, got %d: %v", len(lines), lines)
	}
	if lines[0] != "line2" || lines[1] != "line3" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestTailAppendedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	os.WriteFile(path, []byte(""), 0o644)

	tl := New(path)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var lines []string
	done := make(chan struct{})
	go func() {
		for line := range tl.Lines() {
			lines = append(lines, line)
			if len(lines) == 2 {
				cancel()
			}
		}
		close(done)
	}()

	// Append lines after a short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		f.WriteString("appended1\n")
		f.Close()

		time.Sleep(200 * time.Millisecond)
		f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		f.WriteString("appended2\n")
		f.Close()
	}()

	tl.Tail(ctx, 0)
	<-done

	if len(lines) != 2 {
		t.Fatalf("expected 2 appended lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "appended1" || lines[1] != "appended2" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestTailContextCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	os.WriteFile(path, []byte(""), 0o644)

	tl := New(path)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		tl.Tail(ctx, 0)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// Good, tailer stopped
	case <-time.After(3 * time.Second):
		t.Fatal("tailer did not stop after context cancellation")
	}
}

func TestTailCursorTracking(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	os.WriteFile(path, []byte("line1\nline2\n"), 0o644)

	tl := New(path)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

	var count int
	go func() {
		for range tl.Lines() {
			count++
			if count == 2 {
				cancel()
			}
		}
	}()

	finalOffset, _ := tl.Tail(ctx, 0)

	// "line1\nline2\n" = 12 bytes
	if finalOffset != 12 {
		t.Errorf("expected final offset 12, got %d", finalOffset)
	}
}
