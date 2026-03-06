package codex

import "strings"

type applyPatchFile struct {
	FilePath  string
	MoveTo    string
	Operation string
	Diff      string
}

func parseApplyPatchInput(input string) []applyPatchFile {
	const (
		beginPatchPrefix = "*** Begin Patch"
		endPatchPrefix   = "*** End Patch"
		updateFilePrefix = "*** Update File: "
		addFilePrefix    = "*** Add File: "
		deleteFilePrefix = "*** Delete File: "
		moveToPrefix     = "*** Move to: "
		endOfFilePrefix  = "*** End of File"
	)

	var files []applyPatchFile
	var current *applyPatchFile
	var diffLines []string

	flushCurrent := func() {
		if current == nil {
			return
		}
		current.Diff = strings.Join(diffLines, "\n")
		files = append(files, *current)
		current = nil
		diffLines = diffLines[:0]
	}

	for _, line := range strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n") {
		switch {
		case line == "", line == beginPatchPrefix, line == endOfFilePrefix:
			continue
		case line == endPatchPrefix:
			flushCurrent()
			return files
		case strings.HasPrefix(line, updateFilePrefix):
			flushCurrent()
			current = &applyPatchFile{
				FilePath:  strings.TrimPrefix(line, updateFilePrefix),
				Operation: "update",
			}
		case strings.HasPrefix(line, addFilePrefix):
			flushCurrent()
			current = &applyPatchFile{
				FilePath:  strings.TrimPrefix(line, addFilePrefix),
				Operation: "add",
			}
		case strings.HasPrefix(line, deleteFilePrefix):
			flushCurrent()
			current = &applyPatchFile{
				FilePath:  strings.TrimPrefix(line, deleteFilePrefix),
				Operation: "delete",
			}
		case strings.HasPrefix(line, moveToPrefix):
			if current != nil {
				current.MoveTo = strings.TrimPrefix(line, moveToPrefix)
			}
		case current == nil:
			continue
		default:
			diffLines = append(diffLines, line)
		}
	}

	flushCurrent()
	return files
}
