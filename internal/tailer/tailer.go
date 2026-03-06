package tailer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Cursor tracks the read position in a file.
type Cursor struct {
	Path       string
	Inode      uint64
	ByteOffset int64
}

// Tailer watches a file and emits new complete lines as they are appended.
type Tailer struct {
	path    string
	lines   chan string
	pollInt time.Duration
}

// New creates a new Tailer for the given file path.
func New(path string) *Tailer {
	return &Tailer{
		path:    path,
		lines:   make(chan string, 64),
		pollInt: 500 * time.Millisecond,
	}
}

// Lines returns the channel that receives new lines.
func (t *Tailer) Lines() <-chan string {
	return t.lines
}

// Tail starts tailing the file from the given cursor offset.
// It blocks until the context is cancelled.
// When done, it returns the final cursor position.
func (t *Tailer) Tail(ctx context.Context, offset int64) (int64, error) {
	defer close(t.lines)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return offset, fmt.Errorf("create watcher: %w", err)
	}
	defer watcher.Close()

	if err := watcher.Add(t.path); err != nil {
		// File might not exist yet — poll until it appears
		offset, err = t.waitForFile(ctx, watcher)
		if err != nil {
			return offset, err
		}
	}

	return t.tailLoop(ctx, watcher, offset)
}

func (t *Tailer) waitForFile(ctx context.Context, watcher *fsnotify.Watcher) (int64, error) {
	ticker := time.NewTicker(t.pollInt)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
			if err := watcher.Add(t.path); err == nil {
				return 0, nil
			}
		}
	}
}

func (t *Tailer) tailLoop(ctx context.Context, watcher *fsnotify.Watcher, offset int64) (int64, error) {
	// Read any existing content from offset
	offset = t.readNewLines(offset)

	// Periodic poll as fallback (fsnotify can miss events in some edge cases)
	ticker := time.NewTicker(t.pollInt)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return offset, nil
		case event, ok := <-watcher.Events:
			if !ok {
				return offset, nil
			}
			if event.Has(fsnotify.Write) {
				offset = t.readNewLines(offset)
			}
		case <-watcher.Errors:
			// Log and continue; don't crash on transient errors
			continue
		case <-ticker.C:
			offset = t.readNewLines(offset)
		}
	}
}

func (t *Tailer) readNewLines(offset int64) int64 {
	f, err := os.Open(t.path)
	if err != nil {
		return offset
	}
	defer f.Close()

	// Check if file was truncated/replaced
	info, err := f.Stat()
	if err != nil {
		return offset
	}
	if info.Size() < offset {
		offset = 0 // File was truncated, start over
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 2*1024*1024), 10*1024*1024) // 10MB max line

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			offset += 1 // newline byte
			continue
		}
		t.lines <- line
		offset += int64(len(scanner.Bytes())) + 1 // +1 for newline
	}

	// If scanner hit a token-too-long error, skip past the current file position
	// to avoid re-reading the same oversized line forever.
	if scanner.Err() != nil {
		pos, err := f.Seek(0, io.SeekCurrent)
		if err == nil && pos > offset {
			offset = pos
		}
	}

	return offset
}
