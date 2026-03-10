package jsonl

import (
	"bufio"
	"io"
)

// ReadLine returns the next JSONL record without its trailing line ending.
// bytesRead includes the delimiter bytes so callers can track exact file offsets.
func ReadLine(r *bufio.Reader) ([]byte, int, bool, error) {
	raw, err := r.ReadBytes('\n')
	if err != nil {
		if err == io.EOF && len(raw) > 0 {
			return trimLineEnding(raw), len(raw), false, nil
		}
		return nil, 0, false, err
	}

	return trimLineEnding(raw), len(raw), true, nil
}

func trimLineEnding(line []byte) []byte {
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
	}
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	return line
}
