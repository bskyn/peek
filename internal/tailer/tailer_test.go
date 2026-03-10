package tailer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTailExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	if err := os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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

	if _, err := tl.Tail(ctx, 0); err != nil {
		t.Fatalf("tail: %v", err)
	}
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

	if err := os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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

	if _, err := tl.Tail(ctx, 6); err != nil {
		t.Fatalf("tail: %v", err)
	}
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

	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tl := New(path)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var lines []string
	done := make(chan struct{})
	errCh := make(chan error, 1)
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
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			errCh <- err
			return
		}
		if _, err := f.WriteString("appended1\n"); err != nil {
			_ = f.Close()
			errCh <- err
			return
		}
		if err := f.Close(); err != nil {
			errCh <- err
			return
		}

		time.Sleep(200 * time.Millisecond)
		f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			errCh <- err
			return
		}
		if _, err := f.WriteString("appended2\n"); err != nil {
			_ = f.Close()
			errCh <- err
			return
		}
		if err := f.Close(); err != nil {
			errCh <- err
			return
		}
	}()

	if _, err := tl.Tail(ctx, 0); err != nil {
		t.Fatalf("tail: %v", err)
	}
	<-done
	select {
	case err := <-errCh:
		t.Fatalf("append: %v", err)
	default:
	}

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
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tl := New(path)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		if _, err := tl.Tail(ctx, 0); err != nil {
			t.Errorf("tail: %v", err)
		}
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

	if err := os.WriteFile(path, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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

	finalOffset, err := tl.Tail(ctx, 0)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}

	// "line1\nline2\n" = 12 bytes
	if finalOffset != 12 {
		t.Errorf("expected final offset 12, got %d", finalOffset)
	}
}

func TestTailHandlesOversizedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.jsonl")
	largeLine := strings.Repeat("x", (10*1024*1024)+128)
	content := largeLine + "\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tl := New(path)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var lines []string
	done := make(chan struct{})
	go func() {
		for line := range tl.Lines() {
			lines = append(lines, line)
			cancel()
		}
		close(done)
	}()

	finalOffset, err := tl.Tail(ctx, 0)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	<-done

	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if len(lines[0]) != len(largeLine) {
		t.Fatalf("expected line length %d, got %d", len(largeLine), len(lines[0]))
	}
	if finalOffset != int64(len(content)) {
		t.Fatalf("expected offset %d, got %d", len(content), finalOffset)
	}
}
