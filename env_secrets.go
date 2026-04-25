package keystone

// Spec-declared secret resolution with per-secret source selection.
//
// The spec's `secrets:` block is the single source of truth: each entry
// names the secret AND declares where its value comes from via `source:`.
// Parity with the TypeScript and Python SDKs.
//
// Recognized sources:
//   - env                (default)    → os.Getenv(NAME)
//   - env:OTHER_NAME                  → os.Getenv(OTHER_NAME)    (rename)
//   - file:path                       → trimmed contents of the file
//   - command:<shell>                 → exec shell, capture stdout (trimmed)
//   - dashboard                       → server-side only; SDK does NOT forward
//
// Precedence (highest wins): spec literal (from: static://…) > SDK-
// forwarded source value > Dashboard.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// CollectDeclaredSecrets resolves a parsed spec's declared secrets into a
// {name: value} map suitable for CreateSandboxRequest.Secrets /
// CreateExperimentRequest.Secrets. Pass nil for env to use os environment.
func CollectDeclaredSecrets(spec map[string]any, env map[string]string) map[string]string {
	if env == nil {
		env = osEnvMap()
	}
	out := make(map[string]string)
	secretsRaw, ok := spec["secrets"]
	if !ok || secretsRaw == nil {
		return out
	}
	entries, ok := secretsRaw.([]any)
	if !ok {
		return out
	}
	for _, e := range entries {
		var name, source string
		source = "env"
		hasFrom := false
		switch v := e.(type) {
		case string:
			name = v
		case map[string]any:
			if n, ok := v["name"].(string); ok {
				name = n
			}
			if f, ok := v["from"].(string); ok && f != "" {
				hasFrom = true
			}
			if s, ok := v["source"].(string); ok && s != "" {
				source = s
			}
		}
		if name == "" || hasFrom || source == "dashboard" {
			continue
		}
		val, err := ResolveSource(name, source, env)
		if err != nil || val == "" {
			continue
		}
		out[name] = val
	}
	return out
}

// CollectDeclaredSecretsFromFile parses a YAML spec and resolves its
// declared secrets in one shot.
func CollectDeclaredSecretsFromFile(specPath string) (map[string]string, error) {
	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("read spec %s: %w", specPath, err)
	}
	spec := parseSpecSecrets(string(data))
	return CollectDeclaredSecrets(spec, nil), nil
}

// ResolveSource dispatches a single source descriptor to its resolver.
// Returns ("", nil) when the source produced no value (file missing, env
// unset, etc.) — callers decide whether to treat that as an error.
func ResolveSource(name, source string, env map[string]string) (string, error) {
	if env == nil {
		env = osEnvMap()
	}
	switch {
	case source == "env":
		return env[name], nil
	case strings.HasPrefix(source, "env:"):
		return env[source[4:]], nil
	case strings.HasPrefix(source, "file:"):
		path := expandHome(source[5:])
		data, err := os.ReadFile(path)
		if err != nil {
			return "", nil // treat as unresolved, not fatal
		}
		return strings.TrimSpace(string(data)), nil
	case strings.HasPrefix(source, "command:"):
		cmd := source[8:]
		// Use shell so users can pipe/quote naturally.
		out, err := exec.Command("sh", "-c", cmd).Output()
		if err != nil {
			return "", nil
		}
		return strings.TrimSpace(string(out)), nil
	}
	return "", fmt.Errorf(`unknown secret source "%s" for %s`, source, name)
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// ── Minimal spec parser ────────────────────────────────────────────────────

var (
	inlineArrayRE = regexp.MustCompile(`^secrets:\s*\[([^\]]*)\]\s*$`)
	nameInlineRE  = regexp.MustCompile(`name:\s*['"]?([A-Z0-9_]+)['"]?`)
	fromInlineRE  = regexp.MustCompile(`from:\s*['"]?([^'"\s]+)['"]?`)
	srcInlineRE   = regexp.MustCompile(`source:\s*['"]?([^'"]+?)['"]?\s*$`)
	listItemRE    = regexp.MustCompile(`^\s*-\s*(.*)$`)
	nameKeyRE     = regexp.MustCompile(`^\s+name:\s*['"]?([A-Z0-9_]+)['"]?`)
	fromKeyRE     = regexp.MustCompile(`^\s+from:\s*['"]?([^'"\s]+)['"]?`)
	srcKeyRE      = regexp.MustCompile(`^\s+source:\s*['"]?([^'"]+?)['"]?\s*$`)
)

func parseSpecSecrets(yamlText string) map[string]any {
	spec := map[string]any{"secrets": []any{}}
	lines := strings.Split(yamlText, "\n")

	i := 0
	for i < len(lines) {
		line := lines[i]
		if m := inlineArrayRE.FindStringSubmatch(line); m != nil {
			inner := strings.TrimSpace(m[1])
			if inner == "" {
				return spec
			}
			parts := strings.Split(inner, ",")
			out := make([]any, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				p = strings.Trim(p, `'"`)
				if p != "" {
					out = append(out, p)
				}
			}
			spec["secrets"] = out
			return spec
		}
		if strings.TrimSpace(line) == "secrets:" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			i++
			items := []any{}
			var current map[string]any
			for i < len(lines) {
				l := lines[i]
				if l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
					break
				}
				if m := listItemRE.FindStringSubmatch(l); m != nil {
					if current != nil {
						items = append(items, current)
					}
					current = map[string]any{}
					rest := m[1]
					if nm := nameInlineRE.FindStringSubmatch(rest); nm != nil {
						current["name"] = nm[1]
					}
					if fr := fromInlineRE.FindStringSubmatch(rest); fr != nil {
						current["from"] = fr[1]
					}
					if sr := srcInlineRE.FindStringSubmatch(rest); sr != nil {
						current["source"] = sr[1]
					}
				} else if current != nil {
					if nm := nameKeyRE.FindStringSubmatch(l); nm != nil {
						current["name"] = nm[1]
					}
					if fr := fromKeyRE.FindStringSubmatch(l); fr != nil {
						current["from"] = fr[1]
					}
					if sr := srcKeyRE.FindStringSubmatch(l); sr != nil {
						current["source"] = sr[1]
					}
				}
				i++
			}
			if current != nil {
				items = append(items, current)
			}
			spec["secrets"] = items
			break
		}
		i++
	}
	return spec
}

func osEnvMap() map[string]string {
	out := make(map[string]string)
	for _, e := range os.Environ() {
		if idx := strings.IndexByte(e, '='); idx > 0 {
			out[e[:idx]] = e[idx+1:]
		}
	}
	return out
}
