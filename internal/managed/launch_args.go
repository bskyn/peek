package managed

import (
	"fmt"
	"strings"
)

type managedOptionArity int

const (
	managedOptionNone managedOptionArity = iota
	managedOptionRequired
	managedOptionOptional
	managedOptionVariadic
)

type managedOptionSpec struct {
	arity      managedOptionArity
	disallowed string
}

// PrepareManagedLaunchArgs validates that managed-mode provider args can be
// replayed on branch/switch relaunches without changing command shape.
func PrepareManagedLaunchArgs(source Source, args []string) ([]string, error) {
	specs := managedOptionSpecs(source)
	prepared := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		token := args[i]
		if token == "--" {
			return nil, fmt.Errorf("managed %s launch args must not include bare `--`; pass reusable provider options only", source)
		}

		name, spec, hasInlineValue, ok := resolveManagedOption(specs, token)
		if !ok {
			if !looksLikeManagedOption(token) {
				return nil, fmt.Errorf("managed %s launch args must not include prompts or subcommands (%q); pass reusable provider options only", source, token)
			}
			return nil, fmt.Errorf("managed %s launch option %q is not recognized; use supported flags or --flag=value for newer provider options", source, token)
		}
		if spec.disallowed != "" {
			return nil, fmt.Errorf("managed %s launch option %q is not supported: %s", source, name, spec.disallowed)
		}

		prepared = append(prepared, token)
		switch spec.arity {
		case managedOptionNone:
			continue
		case managedOptionRequired:
			if hasInlineValue {
				continue
			}
			if i+1 >= len(args) || looksLikeManagedOption(args[i+1]) {
				return nil, fmt.Errorf("managed %s launch option %q requires a value", source, name)
			}
			i++
			prepared = append(prepared, args[i])
		case managedOptionOptional:
			if hasInlineValue {
				continue
			}
			if i+1 < len(args) && !looksLikeManagedOption(args[i+1]) {
				i++
				prepared = append(prepared, args[i])
			}
		case managedOptionVariadic:
			if hasInlineValue {
				continue
			}
			if i+1 >= len(args) || looksLikeManagedOption(args[i+1]) {
				return nil, fmt.Errorf("managed %s launch option %q requires at least one value", source, name)
			}
			for i+1 < len(args) && !looksLikeManagedOption(args[i+1]) {
				i++
				prepared = append(prepared, args[i])
			}
		}
	}

	return prepared, nil
}

func resolveManagedOption(specs map[string]managedOptionSpec, token string) (string, managedOptionSpec, bool, bool) {
	if spec, ok := specs[token]; ok {
		return token, spec, false, true
	}

	if strings.HasPrefix(token, "--") {
		if idx := strings.Index(token, "="); idx > 2 {
			if spec, ok := specs[token[:idx]]; ok {
				return token[:idx], spec, true, true
			}
		}
		return "", managedOptionSpec{}, false, false
	}

	if strings.HasPrefix(token, "-") && len(token) > 2 {
		if spec, ok := specs[token[:2]]; ok && spec.arity != managedOptionNone {
			return token[:2], spec, true, true
		}
	}

	return "", managedOptionSpec{}, false, false
}

func looksLikeManagedOption(token string) bool {
	return strings.HasPrefix(token, "-") && token != "-"
}

func managedOptionSpecs(source Source) map[string]managedOptionSpec {
	switch source {
	case SourceClaude:
		return claudeManagedOptionSpecs()
	case SourceCodex:
		return codexManagedOptionSpecs()
	default:
		return map[string]managedOptionSpec{}
	}
}

func claudeManagedOptionSpecs() map[string]managedOptionSpec {
	return map[string]managedOptionSpec{
		"--add-dir":                            {arity: managedOptionVariadic},
		"--agent":                              {arity: managedOptionRequired},
		"--agents":                             {arity: managedOptionRequired},
		"--allow-dangerously-skip-permissions": {arity: managedOptionNone},
		"--allowedTools":                       {arity: managedOptionVariadic},
		"--allowed-tools":                      {arity: managedOptionVariadic},
		"--append-system-prompt":               {arity: managedOptionRequired},
		"--betas":                              {arity: managedOptionVariadic},
		"--brief":                              {arity: managedOptionNone},
		"--chrome":                             {arity: managedOptionNone},
		"-c":                                   {arity: managedOptionNone, disallowed: "Peek manages session continuation itself"},
		"--continue":                           {arity: managedOptionNone, disallowed: "Peek manages session continuation itself"},
		"--dangerously-skip-permissions":       {arity: managedOptionNone},
		"-d":                                   {arity: managedOptionOptional},
		"--debug":                              {arity: managedOptionOptional},
		"--debug-file":                         {arity: managedOptionRequired},
		"--disable-slash-commands":             {arity: managedOptionNone},
		"--disallowedTools":                    {arity: managedOptionVariadic},
		"--disallowed-tools":                   {arity: managedOptionVariadic},
		"--effort":                             {arity: managedOptionRequired},
		"--fallback-model":                     {arity: managedOptionRequired},
		"--file":                               {arity: managedOptionVariadic},
		"--fork-session":                       {arity: managedOptionNone, disallowed: "Peek creates branch sessions itself"},
		"--from-pr":                            {arity: managedOptionOptional, disallowed: "Peek manages session continuation itself"},
		"-h":                                   {arity: managedOptionNone, disallowed: "help exits instead of starting a managed session"},
		"--help":                               {arity: managedOptionNone, disallowed: "help exits instead of starting a managed session"},
		"--ide":                                {arity: managedOptionNone},
		"--include-partial-messages":           {arity: managedOptionNone},
		"--input-format":                       {arity: managedOptionRequired},
		"--json-schema":                        {arity: managedOptionRequired},
		"--max-budget-usd":                     {arity: managedOptionRequired},
		"--mcp-config":                         {arity: managedOptionVariadic},
		"--mcp-debug":                          {arity: managedOptionNone},
		"--model":                              {arity: managedOptionRequired},
		"-n":                                   {arity: managedOptionRequired},
		"--name":                               {arity: managedOptionRequired},
		"--no-chrome":                          {arity: managedOptionNone},
		"--no-session-persistence":             {arity: managedOptionNone, disallowed: "managed relaunches require persisted provider sessions"},
		"--output-format":                      {arity: managedOptionRequired},
		"--permission-mode":                    {arity: managedOptionRequired},
		"--plugin-dir":                         {arity: managedOptionRequired},
		"-p":                                   {arity: managedOptionNone, disallowed: "print mode is non-interactive and cannot own the managed terminal"},
		"--print":                              {arity: managedOptionNone, disallowed: "print mode is non-interactive and cannot own the managed terminal"},
		"--replay-user-messages":               {arity: managedOptionNone},
		"-r":                                   {arity: managedOptionOptional, disallowed: "Peek manages session resumption itself"},
		"--resume":                             {arity: managedOptionOptional, disallowed: "Peek manages session resumption itself"},
		"--session-id":                         {arity: managedOptionRequired, disallowed: "Peek assigns managed session IDs itself"},
		"--setting-sources":                    {arity: managedOptionRequired},
		"--settings":                           {arity: managedOptionRequired},
		"--strict-mcp-config":                  {arity: managedOptionNone},
		"--system-prompt":                      {arity: managedOptionRequired},
		"--tmux":                               {arity: managedOptionNone},
		"--tools":                              {arity: managedOptionVariadic},
		"--verbose":                            {arity: managedOptionNone},
		"-v":                                   {arity: managedOptionNone, disallowed: "version exits instead of starting a managed session"},
		"--version":                            {arity: managedOptionNone, disallowed: "version exits instead of starting a managed session"},
		"-w":                                   {arity: managedOptionOptional, disallowed: "Peek owns the worktree layout in managed mode"},
		"--worktree":                           {arity: managedOptionOptional, disallowed: "Peek owns the worktree layout in managed mode"},
	}
}

func codexManagedOptionSpecs() map[string]managedOptionSpec {
	return map[string]managedOptionSpec{
		"-c":                 {arity: managedOptionRequired},
		"--config":           {arity: managedOptionRequired},
		"--enable":           {arity: managedOptionRequired},
		"--disable":          {arity: managedOptionRequired},
		"-i":                 {arity: managedOptionVariadic},
		"--image":            {arity: managedOptionVariadic},
		"-m":                 {arity: managedOptionRequired},
		"--model":            {arity: managedOptionRequired},
		"--oss":              {arity: managedOptionNone},
		"--local-provider":   {arity: managedOptionRequired},
		"-p":                 {arity: managedOptionRequired},
		"--profile":          {arity: managedOptionRequired},
		"-s":                 {arity: managedOptionRequired},
		"--sandbox":          {arity: managedOptionRequired},
		"-a":                 {arity: managedOptionRequired},
		"--ask-for-approval": {arity: managedOptionRequired},
		"--full-auto":        {arity: managedOptionNone},
		"--dangerously-bypass-approvals-and-sandbox": {arity: managedOptionNone},
		"-C":              {arity: managedOptionRequired, disallowed: "managed mode always runs in the active Peek worktree"},
		"--cd":            {arity: managedOptionRequired, disallowed: "managed mode always runs in the active Peek worktree"},
		"--search":        {arity: managedOptionNone},
		"--add-dir":       {arity: managedOptionRequired},
		"--no-alt-screen": {arity: managedOptionNone},
		"-h":              {arity: managedOptionNone, disallowed: "help exits instead of starting a managed session"},
		"--help":          {arity: managedOptionNone, disallowed: "help exits instead of starting a managed session"},
		"-V":              {arity: managedOptionNone, disallowed: "version exits instead of starting a managed session"},
		"--version":       {arity: managedOptionNone, disallowed: "version exits instead of starting a managed session"},
	}
}
