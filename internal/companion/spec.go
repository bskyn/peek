package companion

import (
	"crypto/sha256"
	"encoding/hex"
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

const ConfigFileName = "peek.runtime.json"

const (
	ServiceRolePrimary = "primary"
	ServiceRoleSupport = "support"

	ProbeTypeHTTP = "http"
	ProbeTypeFile = "file"
)

// ProjectRuntimeSpec describes how Peek should prepare and run workspace-bound
// companion services for a project.
type ProjectRuntimeSpec struct {
	ConfigSource string                 `json:"-"`
	Bootstrap    BootstrapSpec          `json:"bootstrap,omitempty"`
	EnvSources   []EnvSourceSpec        `json:"env_sources,omitempty"`
	Services     []CompanionServiceSpec `json:"services,omitempty"`
	Browser      BrowserTargetSpec      `json:"browser,omitempty"`
}

type BootstrapSpec struct {
	FingerprintPaths []string      `json:"fingerprint_paths,omitempty"`
	Commands         []CommandSpec `json:"commands,omitempty"`
}

type CommandSpec struct {
	Workdir string            `json:"workdir,omitempty"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
}

type EnvSourceSpec struct {
	Path     string `json:"path"`
	Target   string `json:"target,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

type CompanionServiceSpec struct {
	Name      string            `json:"name"`
	Role      string            `json:"role,omitempty"`
	Workdir   string            `json:"workdir,omitempty"`
	Command   []string          `json:"command"`
	Env       map[string]string `json:"env,omitempty"`
	TargetURL string            `json:"target_url,omitempty"`
	Ready     ReadinessProbe    `json:"ready"`
}

type ReadinessProbe struct {
	Type            string `json:"type"`
	URL             string `json:"url,omitempty"`
	Path            string `json:"path,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
	IntervalMillis  int    `json:"interval_millis,omitempty"`
	SuccessContains string `json:"success_contains,omitempty"`
}

type BrowserTargetSpec struct {
	Service    string `json:"service,omitempty"`
	PathPrefix string `json:"path_prefix,omitempty"`
}

// ResolveProjectRuntime loads a repo-local companion spec, falling back to a
// lightweight frontend autodetection path when no explicit config exists.
func ResolveProjectRuntime(projectDir string) (*ProjectRuntimeSpec, error) {
	configPath := filepath.Join(projectDir, ConfigFileName)
	if _, err := os.Stat(configPath); err == nil {
		spec, err := loadProjectRuntime(configPath)
		if err != nil {
			return nil, err
		}
		spec.ConfigSource = configPath
		return spec, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat %s: %w", configPath, err)
	}

	spec, err := autodetectProjectRuntime(projectDir)
	if err != nil {
		return nil, err
	}
	if spec != nil {
		spec.ConfigSource = "autodetect"
	}
	return spec, nil
}

func loadProjectRuntime(configPath string) (*ProjectRuntimeSpec, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", configPath, err)
	}

	var spec ProjectRuntimeSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("validate %s: %w", configPath, err)
	}
	return &spec, nil
}

// Validate ensures the resolved runtime contract is internally consistent.
func (s *ProjectRuntimeSpec) Validate() error {
	if s == nil {
		return nil
	}

	primaryCount := 0
	serviceNames := make(map[string]struct{}, len(s.Services))
	for i, service := range s.Services {
		if service.Name == "" {
			return fmt.Errorf("services[%d].name is required", i)
		}
		if _, exists := serviceNames[service.Name]; exists {
			return fmt.Errorf("duplicate service name %q", service.Name)
		}
		serviceNames[service.Name] = struct{}{}

		role := service.Role
		if role == "" {
			role = ServiceRoleSupport
		}
		if role != ServiceRolePrimary && role != ServiceRoleSupport {
			return fmt.Errorf("service %q has unsupported role %q", service.Name, service.Role)
		}
		if role == ServiceRolePrimary {
			primaryCount++
		}
		if len(service.Command) == 0 {
			return fmt.Errorf("service %q command is required", service.Name)
		}
		if err := validateRelativePath(service.Workdir, fmt.Sprintf("service %q workdir", service.Name)); err != nil {
			return err
		}
		if err := service.Ready.validate(service.Name); err != nil {
			return err
		}
	}

	if len(s.Services) > 0 && primaryCount != 1 {
		return fmt.Errorf("exactly one primary service is required, got %d", primaryCount)
	}

	for i, env := range s.EnvSources {
		if env.Path == "" {
			return fmt.Errorf("env_sources[%d].path is required", i)
		}
		if err := validateRelativePath(env.Path, fmt.Sprintf("env_sources[%d].path", i)); err != nil {
			return err
		}
		target := env.Target
		if target == "" {
			target = env.Path
		}
		if err := validateRelativePath(target, fmt.Sprintf("env_sources[%d].target", i)); err != nil {
			return err
		}
	}

	for i, path := range s.Bootstrap.FingerprintPaths {
		if err := validateRelativePath(path, fmt.Sprintf("bootstrap.fingerprint_paths[%d]", i)); err != nil {
			return err
		}
	}
	for i, cmd := range s.Bootstrap.Commands {
		if len(cmd.Command) == 0 {
			return fmt.Errorf("bootstrap.commands[%d].command is required", i)
		}
		if err := validateRelativePath(cmd.Workdir, fmt.Sprintf("bootstrap.commands[%d].workdir", i)); err != nil {
			return err
		}
	}

	if s.Browser.PathPrefix == "" {
		s.Browser.PathPrefix = "/app/"
	}
	if !strings.HasPrefix(s.Browser.PathPrefix, "/") {
		return fmt.Errorf("browser.path_prefix must start with /")
	}
	if len(s.Services) > 0 {
		if s.Browser.Service == "" {
			for _, service := range s.Services {
				if service.Role == ServiceRolePrimary {
					s.Browser.Service = service.Name
					break
				}
			}
		}
		if s.Browser.Service == "" {
			return fmt.Errorf("browser.service is required when services are configured")
		}
		if _, ok := serviceNames[s.Browser.Service]; !ok {
			return fmt.Errorf("browser.service %q does not match a configured service", s.Browser.Service)
		}
	}

	return nil
}

func (p ReadinessProbe) validate(serviceName string) error {
	if p.Type == "" {
		return fmt.Errorf("service %q readiness.type is required", serviceName)
	}
	if p.Type != ProbeTypeHTTP && p.Type != ProbeTypeFile {
		return fmt.Errorf("service %q readiness.type %q is unsupported", serviceName, p.Type)
	}
	if p.Type == ProbeTypeHTTP && p.URL == "" {
		return fmt.Errorf("service %q readiness.url is required", serviceName)
	}
	if p.Type == ProbeTypeFile && p.Path == "" {
		return fmt.Errorf("service %q readiness.path is required", serviceName)
	}
	return nil
}

func validateRelativePath(value, label string) error {
	if value == "" {
		return nil
	}
	if filepath.IsAbs(value) {
		return fmt.Errorf("%s must be relative, got %q", label, value)
	}
	cleaned := filepath.Clean(value)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("%s escapes the repository root: %q", label, value)
	}
	return nil
}

type packageJSON struct {
	Name         string            `json:"name"`
	Scripts      map[string]string `json:"scripts"`
	Dependencies map[string]string `json:"dependencies"`
	Workspaces   json.RawMessage   `json:"workspaces"`
}

type autodetectCandidate struct {
	relDir     string
	pkg        packageJSON
	score      int
	lockfile   string
	manager    string
	defaultURL string
}

func autodetectProjectRuntime(projectDir string) (*ProjectRuntimeSpec, error) {
	lockfile, manager := detectPackageManager(projectDir)
	if manager == "" {
		return nil, nil
	}

	workspaceGlobs, err := loadWorkspaceGlobs(projectDir)
	if err != nil {
		return nil, err
	}

	var candidates []autodetectCandidate
	err = filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "package.json" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var pkg packageJSON
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil
		}
		if pkg.Scripts == nil || pkg.Scripts["dev"] == "" {
			return nil
		}

		relDir, err := filepath.Rel(projectDir, filepath.Dir(path))
		if err != nil {
			return err
		}
		if relDir == "." {
			relDir = ""
		}
		if relDir != "" && !workspaceMatches(relDir, workspaceGlobs) {
			return nil
		}

		score := 0
		switch {
		case strings.Contains(relDir, "apps/web"), strings.Contains(relDir, "frontend"), strings.HasSuffix(relDir, "web"):
			score = 100
		case strings.Contains(relDir, "apps/"), strings.Contains(relDir, "app"):
			score = 80
		case relDir == "":
			score = 60
		default:
			score = 40
		}

		defaultURL := "http://127.0.0.1:5173/"
		if hasDependency(pkg, "next") {
			defaultURL = "http://127.0.0.1:3000/"
			score += 10
		}

		candidates = append(candidates, autodetectCandidate{
			relDir:     relDir,
			pkg:        pkg,
			score:      score,
			lockfile:   lockfile,
			manager:    manager,
			defaultURL: defaultURL,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("autodetect frontend runtime: %w", err)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].relDir < candidates[j].relDir
		}
		return candidates[i].score > candidates[j].score
	})
	choice := candidates[0]

	installCmd := []string{choice.manager, "install"}
	if choice.manager == "npm" {
		installCmd = []string{"npm", "ci"}
	}
	devCmd := buildDevCommand(choice.manager, choice.relDir)

	envSources := existingEnvSources(projectDir, choice.relDir)
	return &ProjectRuntimeSpec{
		Bootstrap: BootstrapSpec{
			FingerprintPaths: compactPaths([]string{
				choice.lockfile,
				filepath.Join(choice.relDir, "package.json"),
			}),
			Commands: []CommandSpec{{
				Command: installCmd,
			}},
		},
		EnvSources: envSources,
		Services: []CompanionServiceSpec{{
			Name:    "web",
			Role:    ServiceRolePrimary,
			Workdir: choice.relDir,
			Command: devCmd,
			Env: map[string]string{
				"HOST": "127.0.0.1",
			},
			Ready: ReadinessProbe{
				Type:           ProbeTypeHTTP,
				URL:            choice.defaultURL,
				TimeoutSeconds: 45,
				IntervalMillis: 250,
			},
		}},
		Browser: BrowserTargetSpec{
			Service:    "web",
			PathPrefix: "/app/",
		},
	}, nil
}

func loadWorkspaceGlobs(projectDir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read root package.json: %w", err)
	}

	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("parse root package.json: %w", err)
	}
	return extractWorkspaceGlobs(pkg.Workspaces), nil
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

func workspaceMatches(relDir string, globs []string) bool {
	if relDir == "" || len(globs) == 0 {
		return false
	}
	normalizedRelDir := filepath.ToSlash(relDir)
	for _, rawGlob := range globs {
		glob := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(rawGlob)), "./")
		if glob == "" || glob == "." {
			continue
		}
		if strings.HasSuffix(glob, "/**") {
			prefix := strings.TrimSuffix(glob, "/**")
			if normalizedRelDir == prefix || strings.HasPrefix(normalizedRelDir, prefix+"/") {
				return true
			}
		}
		matched, err := path.Match(glob, normalizedRelDir)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func buildDevCommand(manager, relDir string) []string {
	switch manager {
	case "pnpm":
		if relDir == "" {
			return []string{"pnpm", "dev"}
		}
		return []string{"pnpm", "--dir", relDir, "dev"}
	case "npm":
		if relDir == "" {
			return []string{"npm", "run", "dev"}
		}
		return []string{"npm", "--prefix", relDir, "run", "dev"}
	case "yarn":
		if relDir == "" {
			return []string{"yarn", "dev"}
		}
		return []string{"yarn", "--cwd", relDir, "dev"}
	default:
		return []string{manager, "dev"}
	}
}

func detectPackageManager(projectDir string) (lockfile string, manager string) {
	candidates := []struct {
		lockfile string
		manager  string
	}{
		{"pnpm-lock.yaml", "pnpm"},
		{"package-lock.json", "npm"},
		{"yarn.lock", "yarn"},
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(projectDir, candidate.lockfile)); err == nil {
			return candidate.lockfile, candidate.manager
		}
	}
	return "", ""
}

func existingEnvSources(projectDir, relDir string) []EnvSourceSpec {
	paths := []string{
		filepath.Join(relDir, ".env.local"),
		filepath.Join(relDir, ".env.development.local"),
		filepath.Join(relDir, ".env"),
	}
	result := make([]EnvSourceSpec, 0, len(paths))
	for _, candidate := range compactPaths(paths) {
		if _, err := os.Stat(filepath.Join(projectDir, candidate)); err == nil {
			result = append(result, EnvSourceSpec{Path: candidate})
		}
	}
	return result
}

func compactPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	return result
}

func hasDependency(pkg packageJSON, dep string) bool {
	if pkg.Dependencies == nil {
		return false
	}
	_, ok := pkg.Dependencies[dep]
	return ok
}

// FingerprintInputs returns a stable digest for bootstrap-relevant inputs.
func FingerprintInputs(root string, spec *ProjectRuntimeSpec) (string, error) {
	if spec == nil {
		return "", nil
	}

	hasher := sha256.New()
	writeValue := func(value string) {
		_, _ = hasher.Write([]byte(value))
		_, _ = hasher.Write([]byte{0})
	}

	for _, path := range spec.Bootstrap.FingerprintPaths {
		writeValue(path)
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeValue("<missing>")
				continue
			}
			return "", fmt.Errorf("read fingerprint input %s: %w", path, err)
		}
		writeValue(string(data))
	}

	for _, env := range spec.EnvSources {
		writeValue(env.Path)
		data, err := os.ReadFile(filepath.Join(root, env.Path))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && env.Optional {
				writeValue("<optional-missing>")
				continue
			}
			if errors.Is(err, os.ErrNotExist) {
				writeValue("<missing>")
				continue
			}
			return "", fmt.Errorf("read env source %s: %w", env.Path, err)
		}
		writeValue(string(data))
	}

	for _, cmd := range spec.Bootstrap.Commands {
		writeValue(cmd.Workdir)
		for _, arg := range cmd.Command {
			writeValue(arg)
		}
		envKeys := make([]string, 0, len(cmd.Env))
		for key := range cmd.Env {
			envKeys = append(envKeys, key)
		}
		sort.Strings(envKeys)
		for _, key := range envKeys {
			writeValue(key)
			writeValue(cmd.Env[key])
		}
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
