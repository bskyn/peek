package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bskyn/peek/internal/companion"
)

type manifestCreateFlags struct {
	stdout  bool
	force   bool
	service string
}

func newManifestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manifest",
		Short: "Create or inspect repo-local companion manifests",
	}

	cmd.AddCommand(newManifestCreateCmd())
	return cmd
}

func newManifestCreateCmd() *cobra.Command {
	flags := manifestCreateFlags{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Generate a starter peek.runtime.json for the current repo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runManifestCreate(cmd, flags)
		},
	}

	cmd.Flags().BoolVar(&flags.stdout, "stdout", false, "Print the generated manifest to stdout instead of writing a file")
	cmd.Flags().BoolVar(&flags.force, "force", false, "Overwrite an existing peek.runtime.json")
	cmd.Flags().StringVar(&flags.service, "service", "", "Repo-relative service path to use when multiple app candidates exist")
	return cmd
}

func runManifestCreate(cmd *cobra.Command, flags manifestCreateFlags) error {
	if flags.stdout && flags.force {
		return fmt.Errorf("--force cannot be used with --stdout")
	}

	resolution, err := resolveProjectRootFromCWD()
	if err != nil {
		return err
	}
	projectDir := resolution.ProjectRoot

	result, err := companion.CreateManifest(companion.ManifestCreateOptions{
		ProjectDir:  projectDir,
		ServicePath: flags.service,
	})
	if err != nil {
		var ambiguous *companion.AmbiguousManifestError
		if errors.As(err, &ambiguous) {
			return fmt.Errorf("multiple app candidates found; choose one explicitly with --service <path>\n\nCandidates:\n- %s\n\nExample:\n  peek manifest create --service %s", strings.Join(candidateBulletLines(ambiguous.Candidates), "\n- "), firstCandidatePath(ambiguous.Candidates))
		}
		return err
	}

	if flags.stdout {
		_, _ = cmd.OutOrStdout().Write(result.Rendered)
		return nil
	}

	outputPath := filepath.Join(projectDir, companion.ConfigFileName)
	if _, err := os.Stat(outputPath); err == nil && !flags.force {
		return fmt.Errorf("%s already exists; rerun with --force to overwrite or --stdout to preview", companion.ConfigFileName)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", outputPath, err)
	}

	if err := os.WriteFile(outputPath, result.Rendered, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}

	if !resolution.IsRepoRoot {
		fmt.Fprintf(cmd.OutOrStdout(), "Using repo root %s (invoked from %s).\n", resolution.ProjectRoot, resolution.CWD)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Created %s for %s.\n", companion.ConfigFileName, manifestTargetLabel(result.Selected))
	for _, warning := range result.Warnings {
		fmt.Fprintf(cmd.OutOrStdout(), "Warning: %s\n", warning.Message)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Edit the file as needed, then run peek run claude or peek run codex.")
	return nil
}

func candidateBulletLines(candidates []companion.ServiceCandidate) []string {
	lines := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		label := displayServicePath(candidate.Path)
		if candidate.PackageName != "" {
			label = fmt.Sprintf("%s (%s)", label, candidate.PackageName)
		}
		lines = append(lines, label)
	}
	return lines
}

func displayServicePath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "."
	}
	return path
}

func manifestTargetLabel(candidate companion.ServiceCandidate) string {
	path := displayServicePath(candidate.Path)
	if candidate.Path == "" {
		if candidate.PackageName != "" {
			return fmt.Sprintf("the repo root app (%s)", candidate.PackageName)
		}
		return "the repo root app"
	}
	if candidate.PackageName != "" {
		return fmt.Sprintf("%s (%s)", path, candidate.PackageName)
	}
	return path
}

func firstCandidatePath(candidates []companion.ServiceCandidate) string {
	if len(candidates) == 0 {
		return "<path>"
	}
	return displayServicePath(candidates[0].Path)
}
