package keystone

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// ExportService wraps the bulk export / extraction endpoints.
type ExportService struct {
	client *Client
}

// TraceFilter narrows /v1/traces queries.
type TraceFilter struct {
	ExperimentID string
	SandboxID    string
	Agent        string
	EventType    string
	Tool         string
	Since        string
}

// SpanFilter narrows /v1/spans queries.
type SpanFilter struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	RootSpanID   string
	Tool         string
	EventType    string
}

// ScenarioFilter narrows /v1/scenarios queries.
type ScenarioFilter struct {
	ExperimentID string
	Status       string
	ScenarioID   string
}

// ScoreFilter narrows /v1/scores queries.
type ScoreFilter struct {
	ExperimentID string
	RuleID       string
}

// ExportFormat selects the /v1/experiments/{id}/export encoding.
type ExportFormat string

const (
	ExportJSON   ExportFormat = "json"
	ExportNDJSON ExportFormat = "ndjson"
)

// exportPage is the shared response envelope returned by list endpoints.
type exportPage struct {
	Items      json.RawMessage `json:"items"`
	NextCursor string          `json:"next_cursor"`
	Count      int             `json:"count"`
}

// Experiment returns a full experiment dump. When format == ExportJSON the
// payload is decoded into a generic map; ExportNDJSON returns the raw
// newline-delimited JSON as a single string for streaming consumers.
func (s *ExportService) Experiment(ctx context.Context, id string, format ExportFormat) (any, error) {
	path := "/v1/experiments/" + url.PathEscape(id) + "/export"
	if format == ExportNDJSON {
		path += "?format=ndjson"
		data, err := s.client.do(ctx, "GET", path, nil, "")
		if err != nil {
			return nil, err
		}
		return string(data), nil
	}
	data, err := s.client.doJSON(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("keystone: decoding experiment export: %w", err)
	}
	return out, nil
}

// Trace returns every span rooted at the given trace ID.
func (s *ExportService) Trace(ctx context.Context, traceID string) (map[string]json.RawMessage, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/traces/"+url.PathEscape(traceID), nil)
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	return out, json.Unmarshal(data, &out)
}

// Traces returns a channel that yields every trace row matching the filter.
// The channel is closed when iteration completes. Cancel ctx to stop early.
func (s *ExportService) Traces(ctx context.Context, filter TraceFilter, pageSize int) <-chan map[string]any {
	params := []string{}
	if filter.ExperimentID != "" {
		params = append(params, "experiment_id="+url.QueryEscape(filter.ExperimentID))
	}
	if filter.SandboxID != "" {
		params = append(params, "sandbox_id="+url.QueryEscape(filter.SandboxID))
	}
	if filter.Agent != "" {
		params = append(params, "agent="+url.QueryEscape(filter.Agent))
	}
	if filter.EventType != "" {
		params = append(params, "event_type="+url.QueryEscape(filter.EventType))
	}
	if filter.Tool != "" {
		params = append(params, "tool="+url.QueryEscape(filter.Tool))
	}
	if filter.Since != "" {
		params = append(params, "since="+url.QueryEscape(filter.Since))
	}
	return s.paginate(ctx, "/v1/traces", params, pageSize)
}

// Spans returns a channel yielding every span matching the filter.
func (s *ExportService) Spans(ctx context.Context, filter SpanFilter, pageSize int) <-chan map[string]any {
	params := []string{}
	if filter.TraceID != "" {
		params = append(params, "trace_id="+url.QueryEscape(filter.TraceID))
	}
	if filter.SpanID != "" {
		params = append(params, "span_id="+url.QueryEscape(filter.SpanID))
	}
	if filter.ParentSpanID != "" {
		params = append(params, "parent_span_id="+url.QueryEscape(filter.ParentSpanID))
	}
	if filter.RootSpanID != "" {
		params = append(params, "root_span_id="+url.QueryEscape(filter.RootSpanID))
	}
	if filter.Tool != "" {
		params = append(params, "tool="+url.QueryEscape(filter.Tool))
	}
	if filter.EventType != "" {
		params = append(params, "event_type="+url.QueryEscape(filter.EventType))
	}
	return s.paginate(ctx, "/v1/spans", params, pageSize)
}

// Scenarios returns a channel yielding every scenario matching the filter.
func (s *ExportService) Scenarios(ctx context.Context, filter ScenarioFilter, pageSize int) <-chan map[string]any {
	params := []string{}
	if filter.ExperimentID != "" {
		params = append(params, "experiment_id="+url.QueryEscape(filter.ExperimentID))
	}
	if filter.Status != "" {
		params = append(params, "status="+url.QueryEscape(filter.Status))
	}
	if filter.ScenarioID != "" {
		params = append(params, "scenario_id="+url.QueryEscape(filter.ScenarioID))
	}
	return s.paginate(ctx, "/v1/scenarios", params, pageSize)
}

// Scores returns a channel yielding every persisted score row.
func (s *ExportService) Scores(ctx context.Context, filter ScoreFilter, pageSize int) <-chan map[string]any {
	params := []string{}
	if filter.ExperimentID != "" {
		params = append(params, "experiment_id="+url.QueryEscape(filter.ExperimentID))
	}
	if filter.RuleID != "" {
		params = append(params, "rule_id="+url.QueryEscape(filter.RuleID))
	}
	return s.paginate(ctx, "/v1/scores", params, pageSize)
}

// paginate drains a cursor-paginated list endpoint into a channel.
func (s *ExportService) paginate(ctx context.Context, basePath string, baseParams []string, pageSize int) <-chan map[string]any {
	if pageSize <= 0 {
		pageSize = 100
	}
	out := make(chan map[string]any, pageSize)
	go func() {
		defer close(out)
		cursor := ""
		for {
			params := append([]string{}, baseParams...)
			params = append(params, "limit="+strconv.Itoa(pageSize))
			if cursor != "" {
				params = append(params, "cursor="+url.QueryEscape(cursor))
			}
			path := basePath + "?" + joinAmp(params)

			data, err := s.client.doJSON(ctx, "GET", path, nil)
			if err != nil {
				return
			}
			var page exportPage
			if err := json.Unmarshal(data, &page); err != nil {
				return
			}
			var items []map[string]any
			if err := json.Unmarshal(page.Items, &items); err != nil {
				return
			}
			for _, item := range items {
				select {
				case <-ctx.Done():
					return
				case out <- item:
				}
			}
			if page.NextCursor == "" {
				return
			}
			cursor = page.NextCursor
		}
	}()
	return out
}

func joinAmp(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "&"
		}
		out += p
	}
	return out
}
