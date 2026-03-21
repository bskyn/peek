package companion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

type MissingProjectRuntimeError struct {
	ProjectDir     string
	PackageManager PackageManagerKind
	Candidates     []ServiceCandidate
}

func (e *MissingProjectRuntimeError) Error() string {
	if e == nil {
		return ConfigFileName + " is required"
	}
	return fmt.Sprintf("%s is required in %s", ConfigFileName, e.ProjectDir)
}

// ResolveProjectRuntime loads a repo-local companion spec.
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

	return nil, newMissingProjectRuntimeError(projectDir)
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

	if len(s.Services) > 0 {
		if s.Browser.PathPrefix == "" {
			s.Browser.PathPrefix = "/app/"
		}
		if !strings.HasPrefix(s.Browser.PathPrefix, "/") {
			return fmt.Errorf("browser.path_prefix must start with /")
		}
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
	} else if s.Browser.Service != "" || s.Browser.PathPrefix != "" {
		return fmt.Errorf("browser settings require at least one configured service")
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
