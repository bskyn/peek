package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

type projectRootResolution struct {
	CWD         string
	ProjectRoot string
	IsRepoRoot  bool
}

func resolveProjectRootFromCWD() (*projectRootResolution, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	absCWD, err := filepath.Abs(cwd)
	if err != nil {
		absCWD = filepath.Clean(cwd)
	}

	root, found, err := findRepoRoot(absCWD)
	if err != nil {
		return nil, err
	}
	if !found {
		root = absCWD
	}

	return &projectRootResolution{
		CWD:         absCWD,
		ProjectRoot: root,
		IsRepoRoot:  absCWD == root,
	}, nil
}

func findRepoRoot(start string) (string, bool, error) {
	current := start
	for {
		gitPath := filepath.Join(current, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return current, true, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", false, fmt.Errorf("stat %s: %w", gitPath, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", false, nil
		}
		current = parent
	}
}
