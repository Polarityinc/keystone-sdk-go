package keystone

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// ─── Mustache-lite renderer (byte-identical to Go server prompts.go) ────

var (
	promptVarRE      = regexp.MustCompile(`\{\{?\s*(\w+(?:\.\w+)*)\s*\}\}?`)
	promptSectOpenRE = regexp.MustCompile(`\{#\s*(\w+)\s*\}`)
	promptSectCloseRE = regexp.MustCompile(`\{/\s*(\w+)\s*\}`)
)

// RenderTemplate expands {name} / {{name}} interpolation and {#list}..{/list}
// sections over variables. Byte-identical to the Python and TS renderers.
func RenderTemplate(template string, variables map[string]interface{}) string {
	out := expandPromptSections(template, variables)
	return expandPromptVars(out, variables)
}

func expandPromptSections(template string, variables map[string]interface{}) string {
	var buf strings.Builder
	pos := 0
	for pos < len(template) {
		openLoc := promptSectOpenRE.FindStringSubmatchIndex(template[pos:])
		if openLoc == nil {
			buf.WriteString(template[pos:])
			break
		}
		openStart := pos + openLoc[0]
		openEnd := pos + openLoc[1]
		name := template[pos+openLoc[2] : pos+openLoc[3]]

		buf.WriteString(template[pos:openStart])

		closeAbs := findPromptSectionClose(template, openEnd, name)
		if closeAbs < 0 {
			buf.WriteString(template[openStart:openEnd])
			pos = openEnd
			continue
		}
		closeTag := promptSectCloseRE.FindString(template[closeAbs:])
		body := template[openEnd:closeAbs]
		buf.WriteString(renderPromptSection(name, body, variables))
		pos = closeAbs + len(closeTag)
	}
	return buf.String()
}

func findPromptSectionClose(template string, start int, name string) int {
	depth := 1
	i := start
	for i < len(template) {
		openLoc := promptSectOpenRE.FindStringSubmatchIndex(template[i:])
		closeLoc := promptSectCloseRE.FindStringSubmatchIndex(template[i:])
		if closeLoc == nil {
			return -1
		}
		closeName := template[i+closeLoc[2] : i+closeLoc[3]]
		closeAbs := i + closeLoc[0]

		if openLoc != nil && openLoc[0] < closeLoc[0] {
			openName := template[i+openLoc[2] : i+openLoc[3]]
			if openName == name {
				depth++
			}
			i = i + openLoc[1]
			continue
		}
		if closeName == name {
			depth--
			if depth == 0 {
				return closeAbs
			}
		}
		i = i + closeLoc[1]
	}
	return -1
}

func renderPromptSection(name, body string, variables map[string]interface{}) string {
	value, ok := variables[name]
	if !ok {
		return ""
	}
	if list, ok := value.([]interface{}); ok {
		var buf strings.Builder
		for _, item := range list {
			local := make(map[string]interface{}, len(variables)+1)
			for k, v := range variables {
				local[k] = v
			}
			local["_it"] = item
			if m, ok := item.(map[string]interface{}); ok {
				for k, v := range m {
					local[k] = v
				}
			}
			buf.WriteString(expandPromptVars(body, local))
		}
		return buf.String()
	}
	if isPromptFalsy(value) {
		return ""
	}
	return expandPromptVars(body, variables)
}

func expandPromptVars(body string, variables map[string]interface{}) string {
	return promptVarRE.ReplaceAllStringFunc(body, func(match string) string {
		sub := promptVarRE.FindStringSubmatch(match)
		if len(sub) != 2 {
			return match
		}
		path := strings.Split(sub[1], ".")
		v := lookupPromptPath(variables, path)
		if v == nil {
			return match
		}
		return fmt.Sprintf("%v", v)
	})
}

func lookupPromptPath(root map[string]interface{}, path []string) interface{} {
	var cur interface{} = root
	for _, seg := range path {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		v, present := m[seg]
		if !present {
			return nil
		}
		cur = v
	}
	return cur
}

func isPromptFalsy(v interface{}) bool {
	switch t := v.(type) {
	case nil:
		return true
	case bool:
		return !t
	case string:
		return t == ""
	case int, int64, float64:
		return t == 0
	case []interface{}:
		return len(t) == 0
	case map[string]interface{}:
		return len(t) == 0
	}
	return false
}

// ─── Prompt record + Service ────────────────────────────────────────────

// Prompt is a single stored prompt. Use Render to expand variables.
type Prompt struct {
	ID        string                 `json:"id"`
	UserID    string                 `json:"user_id,omitempty"`
	Slug      string                 `json:"slug"`
	Version   int                    `json:"version"`
	Tag       string                 `json:"tag,omitempty"`
	Template  string                 `json:"template"`
	Variables map[string]interface{} `json:"variables,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt string                 `json:"created_at,omitempty"`
}

// Render expands the stored template with overrides merged over Variables.
func (p *Prompt) Render(overrides map[string]interface{}) string {
	merged := make(map[string]interface{}, len(p.Variables)+len(overrides))
	for k, v := range p.Variables {
		merged[k] = v
	}
	for k, v := range overrides {
		merged[k] = v
	}
	return RenderTemplate(p.Template, merged)
}

// PromptService provides CRUD + versioning + templating for prompts.
type PromptService struct {
	client *Client
}

// CreatePromptOpts is the payload for PromptService.Create.
type CreatePromptOpts struct {
	Slug      string
	Template  string
	Tag       string
	Variables map[string]interface{}
	Metadata  map[string]interface{}
}

// Create stores a new prompt, auto-incrementing its version.
func (s *PromptService) Create(ctx context.Context, opts CreatePromptOpts) (*Prompt, error) {
	body := map[string]interface{}{
		"slug":     opts.Slug,
		"template": opts.Template,
	}
	if opts.Tag != "" {
		body["tag"] = opts.Tag
	}
	if len(opts.Variables) > 0 {
		body["variables"] = opts.Variables
	}
	if len(opts.Metadata) > 0 {
		body["metadata"] = opts.Metadata
	}
	data, err := s.client.doJSON(ctx, "POST", "/v1/prompts", body)
	if err != nil {
		return nil, err
	}
	var p Prompt
	return &p, json.Unmarshal(data, &p)
}

// Get fetches a prompt. Without version or tag, returns the latest.
func (s *PromptService) Get(ctx context.Context, slug string, opts ...GetPromptOpt) (*Prompt, error) {
	var o getPromptOpts
	for _, fn := range opts {
		fn(&o)
	}
	enc := url.PathEscape
	var path string
	if o.version != nil {
		path = fmt.Sprintf("/v1/prompts/%s/versions/%d", enc(slug), *o.version)
	} else if o.tag != "" {
		path = fmt.Sprintf("/v1/prompts/%s/tags/%s", enc(slug), enc(o.tag))
	} else {
		path = fmt.Sprintf("/v1/prompts/%s/latest", enc(slug))
	}
	data, err := s.client.doJSON(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var p Prompt
	return &p, json.Unmarshal(data, &p)
}

// GetPromptOpt configures PromptService.Get.
type GetPromptOpt func(*getPromptOpts)

type getPromptOpts struct {
	version *int
	tag     string
}

// PromptVersion pins Get to a specific version.
func PromptVersion(v int) GetPromptOpt { return func(o *getPromptOpts) { o.version = &v } }

// PromptTag pins Get to the version pointed at by a tag.
func PromptTag(t string) GetPromptOpt { return func(o *getPromptOpts) { o.tag = t } }

// List returns every prompt owned by the caller.
func (s *PromptService) List(ctx context.Context) ([]*Prompt, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/prompts", nil)
	if err != nil {
		return nil, err
	}
	var prompts []*Prompt
	return prompts, json.Unmarshal(data, &prompts)
}

// Delete removes a prompt by ID.
func (s *PromptService) Delete(ctx context.Context, id string) error {
	_, err := s.client.doJSON(ctx, "DELETE", "/v1/prompts/"+url.PathEscape(id), nil)
	return err
}

// RenderRemote asks the server to render a stored prompt with the supplied
// variables. Useful when you want the canonical renderer output (server and
// SDK agree but server-rendered output is the audit record).
func (s *PromptService) RenderRemote(ctx context.Context, id string, variables map[string]interface{}) (string, error) {
	body := map[string]interface{}{"variables": variables}
	data, err := s.client.doJSON(ctx, "POST", "/v1/prompts/"+url.PathEscape(id)+"/render", body)
	if err != nil {
		return "", err
	}
	var out struct {
		Rendered string `json:"rendered"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	return out.Rendered, nil
}
