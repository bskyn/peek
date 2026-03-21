package companion

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type PackageManagerKind string

const (
	PackageManagerUnknown PackageManagerKind = ""
	PackageManagerPNPM    PackageManagerKind = "pnpm"
	PackageManagerNPM     PackageManagerKind = "npm"
	PackageManagerYarn    PackageManagerKind = "yarn"
	PackageManagerBun     PackageManagerKind = "bun"
)

type RepoKind string

const (
	RepoKindSinglePackage RepoKind = "single-package"
	RepoKindWorkspace     RepoKind = "workspace"
)

type workspacePatterns struct {
	Includes []string
	Excludes []string
}

type ProjectInspection struct {
	ProjectDir           string
	RepoKind             RepoKind
	PackageManager       PackageManagerKind
	PackageManagerSource string
	LockfilePath         string
	WorkspacePatterns    workspacePatterns
	Candidates           []ServiceCandidate
	rootPackagePresent   bool
}

type ServiceCandidate struct {
	Path            string
	PackageName     string
	PackageJSONPath string
	PackageManager  PackageManagerKind
	LockfilePath    string
	Framework       string
	Score           int
	ReadyURL        string
}

type ManifestCreateOptions struct {
	ProjectDir  string
	ServicePath string
}

type ManifestWarning struct {
	Message string
}

type ManifestCreateResult struct {
	Spec     *ProjectRuntimeSpec
	Rendered []byte
	Selected ServiceCandidate
	Template string
	Warnings []ManifestWarning
}

type AmbiguousManifestError struct {
	Candidates []ServiceCandidate
}

func (e *AmbiguousManifestError) Error() string {
	if e == nil || len(e.Candidates) == 0 {
		return "multiple candidate services found"
	}
	paths := make([]string, 0, len(e.Candidates))
	for _, candidate := range e.Candidates {
		paths = append(paths, candidate.Path)
	}
	return fmt.Sprintf("multiple candidate services found: %s", strings.Join(paths, ", "))
}

type packageJSON struct {
	Name            string            `json:"name"`
	PackageManager  string            `json:"packageManager"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Workspaces      json.RawMessage   `json:"workspaces"`
}

func InspectProject(projectDir string) (*ProjectInspection, error) {
	rootPkg, rootPkgPresent, err := loadRootPackageJSON(projectDir)
	if err != nil {
		return nil, err
	}

	manager, source, lockfile := detectPackageManager(projectDir, rootPkg)
	workspacePatterns, err := loadWorkspacePatterns(projectDir, rootPkg)
	if err != nil {
		return nil, err
	}

	repoKind := RepoKindSinglePackage
	if len(workspacePatterns.Includes) > 0 {
		repoKind = RepoKindWorkspace
	}

	candidates, err := discoverServiceCandidates(projectDir, repoKind, workspacePatterns, rootPkg, rootPkgPresent)
	if err != nil {
		return nil, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			if candidates[i].Path == candidates[j].Path {
				return candidates[i].PackageName < candidates[j].PackageName
			}
			return candidates[i].Path < candidates[j].Path
		}
		return candidates[i].Score > candidates[j].Score
	})

	return &ProjectInspection{
		ProjectDir:           projectDir,
		RepoKind:             repoKind,
		PackageManager:       manager,
		PackageManagerSource: source,
		LockfilePath:         lockfile,
		WorkspacePatterns:    workspacePatterns,
		Candidates:           candidates,
		rootPackagePresent:   rootPkgPresent,
	}, nil
}

func CreateManifest(opts ManifestCreateOptions) (*ManifestCreateResult, error) {
	inspection, err := InspectProject(opts.ProjectDir)
	if err != nil {
		return nil, err
	}
	candidate, selected, err := selectManifestCandidate(inspection, opts.ServicePath)
	if err != nil {
		return nil, err
	}

	spec, template, warnings, err := buildGeneratedRuntimeSpec(inspection, candidate, selected)
	if err != nil {
		return nil, err
	}
	rendered, err := renderProjectRuntimeSpec(spec)
	if err != nil {
		return nil, err
	}

	return &ManifestCreateResult{
		Spec:     spec,
		Rendered: rendered,
		Selected: candidate,
		Template: template,
		Warnings: warnings,
	}, nil
}

func newMissingProjectRuntimeError(projectDir string) error {
	err := &MissingProjectRuntimeError{ProjectDir: projectDir}
	inspection, inspectErr := InspectProject(projectDir)
	if inspectErr != nil || inspection == nil {
		return err
	}
	err.PackageManager = inspection.PackageManager
	err.Candidates = append(err.Candidates, inspection.Candidates...)
	return err
}

func loadRootPackageJSON(projectDir string) (*packageJSON, bool, error) {
	data, err := os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read root package.json: %w", err)
	}

	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, false, fmt.Errorf("parse root package.json: %w", err)
	}
	return &pkg, true, nil
}

func detectPackageManager(projectDir string, rootPkg *packageJSON) (PackageManagerKind, string, string) {
	return detectPackageManagerForDir(projectDir, "", rootPkg)
}

func detectPackageManagerForDir(projectDir, relDir string, pkg *packageJSON) (PackageManagerKind, string, string) {
	manager := parsePackageManagerField("")
	if pkg != nil {
		manager = parsePackageManagerField(pkg.PackageManager)
	}
	lockfiles := presentLockfiles(projectDir, relDir)
	if manager != PackageManagerUnknown {
		for _, candidate := range lockfiles {
			if candidate.kind == manager {
				return manager, "package.json#packageManager", candidate.path
			}
		}
		return manager, "package.json#packageManager", ""
	}
	if len(lockfiles) == 0 {
		return PackageManagerUnknown, "", ""
	}
	return lockfiles[0].kind, lockfiles[0].path, lockfiles[0].path
}

func parsePackageManagerField(raw string) PackageManagerKind {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return PackageManagerUnknown
	}
	name, _, found := strings.Cut(value, "@")
	if !found {
		name = value
	}
	switch PackageManagerKind(name) {
	case PackageManagerPNPM, PackageManagerNPM, PackageManagerYarn, PackageManagerBun:
		return PackageManagerKind(name)
	default:
		return PackageManagerUnknown
	}
}

func presentLockfiles(projectDir, relDir string) []struct {
	path string
	kind PackageManagerKind
} {
	candidates := []struct {
		path string
		kind PackageManagerKind
	}{
		{path: "pnpm-lock.yaml", kind: PackageManagerPNPM},
		{path: "package-lock.json", kind: PackageManagerNPM},
		{path: "yarn.lock", kind: PackageManagerYarn},
		{path: "bun.lock", kind: PackageManagerBun},
		{path: "bun.lockb", kind: PackageManagerBun},
	}

	result := make([]struct {
		path string
		kind PackageManagerKind
	}, 0, len(candidates))
	for _, candidate := range candidates {
		candidatePath := joinRepoPath(relDir, candidate.path)
		if _, err := os.Stat(filepath.Join(projectDir, filepath.FromSlash(candidatePath))); err == nil {
			result = append(result, struct {
				path string
				kind PackageManagerKind
			}{path: candidatePath, kind: candidate.kind})
		}
	}
	return result
}

func loadWorkspacePatterns(projectDir string, rootPkg *packageJSON) (workspacePatterns, error) {
	var globs []string
	if rootPkg != nil {
		globs = append(globs, extractWorkspaceGlobs(rootPkg.Workspaces)...)
	}
	pnpmGlobs, err := loadPNPMWorkspaceGlobs(projectDir)
	if err != nil {
		return workspacePatterns{}, err
	}
	globs = append(globs, pnpmGlobs...)
	return splitWorkspacePatterns(globs), nil
}

func loadPNPMWorkspaceGlobs(projectDir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(projectDir, "pnpm-workspace.yaml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read pnpm-workspace.yaml: %w", err)
	}

	var globs []string
	inPackages := false
	for _, rawLine := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !inPackages {
			if trimmed == "packages:" {
				inPackages = true
			}
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			value = strings.Trim(value, `"'`)
			if value != "" {
				globs = append(globs, value)
			}
			continue
		}
		if !strings.HasPrefix(rawLine, " ") && !strings.HasPrefix(rawLine, "\t") {
			break
		}
	}
	return compactPaths(globs), nil
}

func extractWorkspaceGlobs(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	var direct []string
	if err := json.Unmarshal(raw, &direct); err == nil {
		return compactPaths(direct)
	}

	var nested struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(raw, &nested); err == nil {
		return compactPaths(nested.Packages)
	}

	return nil
}

func discoverServiceCandidates(projectDir string, repoKind RepoKind, patterns workspacePatterns, rootPkg *packageJSON, rootPkgPresent bool) ([]ServiceCandidate, error) {
	candidates := make([]ServiceCandidate, 0, 4)
	if rootPkgPresent && hasDevScript(*rootPkg) {
		candidates = append(candidates, buildServiceCandidate(projectDir, "", *rootPkg))
	}
	if repoKind != RepoKindWorkspace {
		return candidates, nil
	}

	err := filepath.WalkDir(projectDir, func(currentPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "package.json" || currentPath == filepath.Join(projectDir, "package.json") {
			return nil
		}

		data, err := os.ReadFile(currentPath)
		if err != nil {
			return err
		}
		var pkg packageJSON
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil
		}
		if !hasDevScript(pkg) {
			return nil
		}

		relDir, err := filepath.Rel(projectDir, filepath.Dir(currentPath))
		if err != nil {
			return err
		}
		relDir = normalizeRepoPath(relDir)
		if !workspaceMatches(relDir, patterns) {
			return nil
		}

		candidates = append(candidates, buildServiceCandidate(projectDir, relDir, pkg))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("inspect project packages: %w", err)
	}
	return candidates, nil
}

func hasDevScript(pkg packageJSON) bool {
	return pkg.Scripts != nil && strings.TrimSpace(pkg.Scripts["dev"]) != ""
}

func buildServiceCandidate(projectDir, relDir string, pkg packageJSON) ServiceCandidate {
	framework := detectFramework(pkg)
	readyURL := defaultReadyURL(framework)
	manager, _, lockfile := detectPackageManagerForDir(projectDir, relDir, &pkg)
	return ServiceCandidate{
		Path:            relDir,
		PackageName:     pkg.Name,
		PackageJSONPath: joinRepoPath(relDir, "package.json"),
		PackageManager:  manager,
		LockfilePath:    lockfile,
		Framework:       framework,
		Score:           candidateScore(relDir, pkg, framework),
		ReadyURL:        readyURL,
	}
}

func loadPackageJSON(projectDir, relDir string) (packageJSON, bool, error) {
	fullPath := filepath.Join(projectDir, filepath.FromSlash(joinRepoPath(relDir, "package.json")))
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return packageJSON{}, false, nil
		}
		return packageJSON{}, false, fmt.Errorf("read %s: %w", joinRepoPath(relDir, "package.json"), err)
	}

	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return packageJSON{}, false, nil
	}
	return pkg, true, nil
}

func detectFramework(pkg packageJSON) string {
	switch {
	case hasDependency(pkg, "next"):
		return "next"
	case hasDependency(pkg, "astro"):
		return "astro"
	case hasDependency(pkg, "@remix-run/dev") && hasDependency(pkg, "vite"):
		return "remix-vite"
	case hasDependency(pkg, "@remix-run/dev"):
		return "remix"
	case hasDependency(pkg, "@sveltejs/kit"):
		return "sveltekit"
	case hasDependency(pkg, "vite"):
		return "vite"
	case hasDependency(pkg, "react-scripts"):
		return "react-scripts"
	default:
		return ""
	}
}

func defaultReadyURL(framework string) string {
	switch framework {
	case "next", "react-scripts", "remix":
		return "http://127.0.0.1:3000/"
	case "astro":
		return "http://127.0.0.1:4321/"
	default:
		return "http://127.0.0.1:5173/"
	}
}

func candidateScore(relDir string, pkg packageJSON, framework string) int {
	score := 0
	base := path.Base(relDir)
	switch {
	case relDir == "apps/web":
		score = 140
	case hasPathSegment(relDir, "frontend"):
		score = 130
	case base == "web":
		score = 125
	case strings.HasPrefix(relDir, "apps/"):
		score = 115
	case hasPathSegment(relDir, "app"), hasPathSegment(relDir, "site"), hasPathSegment(relDir, "client"):
		score = 105
	case relDir == "":
		score = 95
	default:
		score = 80
	}

	switch framework {
	case "next":
		score += 15
	case "astro", "remix", "remix-vite", "sveltekit", "vite", "react-scripts":
		score += 10
	}

	name := strings.ToLower(pkg.Name)
	switch {
	case strings.Contains(name, "web"), strings.Contains(name, "frontend"):
		score += 5
	case strings.Contains(name, "app"), strings.Contains(name, "site"), strings.Contains(name, "client"):
		score += 3
	}

	return score
}

func hasPathSegment(relDir, segment string) bool {
	for _, part := range strings.Split(relDir, "/") {
		if part == segment {
			return true
		}
	}
	return false
}

func workspaceMatches(relDir string, patterns workspacePatterns) bool {
	if relDir == "" || len(patterns.Includes) == 0 {
		return false
	}
	included := false
	for _, glob := range patterns.Includes {
		if pathMatchesWorkspaceGlob(relDir, glob) {
			included = true
			break
		}
	}
	if !included {
		return false
	}
	for _, glob := range patterns.Excludes {
		if pathMatchesWorkspaceGlob(relDir, glob) {
			return false
		}
	}
	return true
}

func selectManifestCandidate(inspection *ProjectInspection, servicePath string) (ServiceCandidate, bool, error) {
	if inspection == nil {
		return ServiceCandidate{}, false, fmt.Errorf("project inspection is required")
	}

	if strings.TrimSpace(servicePath) != "" {
		if err := validateRelativePath(servicePath, "--service"); err != nil {
			return ServiceCandidate{}, false, err
		}
		normalized := normalizeRepoPath(servicePath)
		for _, candidate := range inspection.Candidates {
			if candidate.Path == normalized {
				return candidate, true, nil
			}
		}
		pkg, ok, err := loadPackageJSON(inspection.ProjectDir, normalized)
		if err != nil {
			return ServiceCandidate{}, false, err
		}
		if ok && hasDevScript(pkg) {
			return buildServiceCandidate(inspection.ProjectDir, normalized, pkg), true, nil
		}
		if len(inspection.Candidates) > 0 {
			return ServiceCandidate{}, false, fmt.Errorf("service %q was not found; available candidates: %s", displayRepoPath(normalized), strings.Join(candidatePaths(inspection.Candidates), ", "))
		}
		return ServiceCandidate{Path: normalized}, false, nil
	}

	if len(inspection.Candidates) > 1 {
		return ServiceCandidate{}, false, &AmbiguousManifestError{Candidates: inspection.Candidates}
	}
	if len(inspection.Candidates) == 1 {
		return inspection.Candidates[0], true, nil
	}
	return ServiceCandidate{}, false, nil
}

func candidatePaths(candidates []ServiceCandidate) []string {
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, displayRepoPath(candidate.Path))
	}
	return paths
}

func buildGeneratedRuntimeSpec(inspection *ProjectInspection, candidate ServiceCandidate, selected bool) (*ProjectRuntimeSpec, string, []ManifestWarning, error) {
	if manager, lockfilePath, installWorkdir, ok := resolveNodeScaffold(inspection, candidate, selected); ok {
		spec, warnings, err := buildNodeRuntimeSpec(inspection, candidate, manager, lockfilePath, installWorkdir)
		return spec, "node", warnings, err
	}

	spec, warnings, err := buildGenericRuntimeSpec(candidate)
	return spec, "generic", warnings, err
}

func resolveNodeScaffold(inspection *ProjectInspection, candidate ServiceCandidate, selected bool) (PackageManagerKind, string, string, bool) {
	if !selected || inspection == nil {
		return PackageManagerUnknown, "", "", false
	}
	if candidate.PackageManager != PackageManagerUnknown {
		return candidate.PackageManager, candidate.LockfilePath, candidate.Path, true
	}
	if inspection.PackageManager == PackageManagerUnknown {
		return PackageManagerUnknown, "", "", false
	}
	return inspection.PackageManager, inspection.LockfilePath, "", true
}

func buildNodeRuntimeSpec(inspection *ProjectInspection, candidate ServiceCandidate, manager PackageManagerKind, lockfilePath, installWorkdir string) (*ProjectRuntimeSpec, []ManifestWarning, error) {
	serviceName := serviceNameForCandidate(candidate)
	spec := &ProjectRuntimeSpec{
		Bootstrap: BootstrapSpec{
			FingerprintPaths: compactPaths([]string{
				lockfilePath,
				rootPackageJSONPath(inspection),
				candidate.PackageJSONPath,
			}),
			Commands: []CommandSpec{{
				Workdir: installWorkdir,
				Command: buildInstallCommand(manager, lockfilePath != ""),
			}},
		},
		EnvSources: existingEnvSources(inspection.ProjectDir, candidate.Path),
		Services: []CompanionServiceSpec{{
			Name:    serviceName,
			Role:    ServiceRolePrimary,
			Workdir: candidate.Path,
			Command: buildDevCommand(manager),
			Env: map[string]string{
				"HOST": "127.0.0.1",
			},
			Ready: ReadinessProbe{
				Type:           ProbeTypeHTTP,
				URL:            candidate.ReadyURL,
				TimeoutSeconds: 45,
				IntervalMillis: 250,
			},
		}},
		Browser: BrowserTargetSpec{
			Service:    serviceName,
			PathPrefix: "/app/",
		},
	}
	if err := spec.Validate(); err != nil {
		return nil, nil, fmt.Errorf("validate generated manifest: %w", err)
	}

	var warnings []ManifestWarning
	if len(spec.EnvSources) == 0 {
		warnings = append(warnings, ManifestWarning{Message: "no existing .env files were detected; add env_sources if your app needs them"})
	}
	return spec, warnings, nil
}

func buildGenericRuntimeSpec(candidate ServiceCandidate) (*ProjectRuntimeSpec, []ManifestWarning, error) {
	spec := &ProjectRuntimeSpec{}
	if err := spec.Validate(); err != nil {
		return nil, nil, fmt.Errorf("validate generated manifest: %w", err)
	}

	warnings := []ManifestWarning{
		{Message: "generic starter manifest generated; managed runs will not start an app until you add a services entry"},
		{Message: "add bootstrap commands, env_sources, and browser settings only if this repo needs a managed companion app"},
	}
	return spec, warnings, nil
}

func renderProjectRuntimeSpec(spec *ProjectRuntimeSpec) ([]byte, error) {
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	data = append(data, '\n')
	return data, nil
}

func rootPackageJSONPath(inspection *ProjectInspection) string {
	if inspection != nil && inspection.rootPackagePresent {
		return "package.json"
	}
	return ""
}

func serviceNameForCandidate(candidate ServiceCandidate) string {
	name := ""
	if candidate.Path != "" {
		name = path.Base(candidate.Path)
	}
	if strings.TrimSpace(name) == "" {
		name = candidate.PackageName
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if candidate.Path == "" && name == "" {
		name = "app"
	}
	var b strings.Builder
	prevDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '-', r == '_':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		if candidate.Path == "" {
			return "app"
		}
		return strings.Trim(path.Base(candidate.Path), "-")
	}
	return result
}

func buildInstallCommand(manager PackageManagerKind, hasLockfile bool) []string {
	switch manager {
	case PackageManagerPNPM:
		return []string{"pnpm", "install"}
	case PackageManagerNPM:
		if hasLockfile {
			return []string{"npm", "ci"}
		}
		return []string{"npm", "install"}
	case PackageManagerYarn:
		return []string{"yarn", "install"}
	case PackageManagerBun:
		return []string{"bun", "install"}
	default:
		return nil
	}
}

func buildDevCommand(manager PackageManagerKind) []string {
	switch manager {
	case PackageManagerPNPM:
		return []string{"pnpm", "dev"}
	case PackageManagerNPM:
		return []string{"npm", "run", "dev"}
	case PackageManagerYarn:
		return []string{"yarn", "dev"}
	case PackageManagerBun:
		return []string{"bun", "run", "dev"}
	default:
		return nil
	}
}

func existingEnvSources(projectDir, relDir string) []EnvSourceSpec {
	paths := []string{
		joinRepoPath(relDir, ".env.local"),
		joinRepoPath(relDir, ".env.development.local"),
		joinRepoPath(relDir, ".env"),
		".env.local",
		".env.development.local",
		".env",
	}
	result := make([]EnvSourceSpec, 0, len(paths))
	for _, candidate := range compactPaths(paths) {
		if _, err := os.Stat(filepath.Join(projectDir, filepath.FromSlash(candidate))); err == nil {
			result = append(result, EnvSourceSpec{Path: candidate})
		}
	}
	return result
}

func compactPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, raw := range paths {
		normalized := normalizeRepoPath(raw)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

func normalizeRepoPath(value string) string {
	if value == "" || value == "." {
		return ""
	}
	cleaned := filepath.Clean(filepath.FromSlash(value))
	if cleaned == "." {
		return ""
	}
	return filepath.ToSlash(cleaned)
}

func displayRepoPath(value string) string {
	normalized := normalizeRepoPath(value)
	if normalized == "" {
		return "."
	}
	return normalized
}

func splitWorkspacePatterns(raw []string) workspacePatterns {
	var patterns workspacePatterns
	for _, glob := range compactPaths(raw) {
		if strings.HasPrefix(glob, "!") {
			exclude := normalizeWorkspaceGlob(strings.TrimPrefix(glob, "!"))
			if exclude != "" {
				patterns.Excludes = append(patterns.Excludes, exclude)
			}
			continue
		}
		include := normalizeWorkspaceGlob(glob)
		if include != "" {
			patterns.Includes = append(patterns.Includes, include)
		}
	}
	return patterns
}

func normalizeWorkspaceGlob(raw string) string {
	glob := strings.TrimPrefix(path.Clean(strings.ReplaceAll(raw, `\`, `/`)), "./")
	if glob == "" || glob == "." {
		return ""
	}
	return glob
}

func pathMatchesWorkspaceGlob(relDir, glob string) bool {
	if glob == "" {
		return false
	}
	if strings.HasSuffix(glob, "/**") {
		prefix := strings.TrimSuffix(glob, "/**")
		if relDir == prefix || strings.HasPrefix(relDir, prefix+"/") {
			return true
		}
	}
	matched, err := path.Match(glob, relDir)
	return err == nil && matched
}

func joinRepoPath(dir, name string) string {
	switch {
	case dir == "":
		return normalizeRepoPath(name)
	case name == "":
		return normalizeRepoPath(dir)
	default:
		return normalizeRepoPath(filepath.Join(filepath.FromSlash(dir), filepath.FromSlash(name)))
	}
}

func hasDependency(pkg packageJSON, dep string) bool {
	if pkg.Dependencies != nil {
		if _, ok := pkg.Dependencies[dep]; ok {
			return true
		}
	}
	if pkg.DevDependencies != nil {
		_, ok := pkg.DevDependencies[dep]
		return ok
	}
	return false
}
