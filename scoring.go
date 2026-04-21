package keystone

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// ScoringService manages score rules and offline scoring.
type ScoringService struct {
	client *Client
}

// ScoreRuleInfo is a score rule returned by the API.
type ScoreRuleInfo struct {
	ID        string         `json:"id"`
	UserID    string         `json:"user_id"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Config    map[string]any `json:"config"`
	CreatedAt string         `json:"created_at,omitempty"`
}

// TraceScoreInfo is a score result.
type TraceScoreInfo struct {
	ID           int64   `json:"id,omitempty"`
	RuleID       string  `json:"rule_id"`
	TraceID      int64   `json:"trace_id"`
	ExperimentID string  `json:"experiment_id,omitempty"`
	Score        float64 `json:"score"`
	Passed       bool    `json:"passed"`
	Message      string  `json:"message,omitempty"`
}

// CreateRule creates a new score rule.
func (s *ScoringService) CreateRule(ctx context.Context, name, ruleType string, config map[string]any) (*ScoreRuleInfo, error) {
	data, err := s.client.doJSON(ctx, "POST", "/v1/score-rules", map[string]any{
		"name": name, "type": ruleType, "config": config,
	})
	if err != nil {
		return nil, err
	}
	var rule ScoreRuleInfo
	return &rule, json.Unmarshal(data, &rule)
}

// ListRules lists all score rules.
func (s *ScoringService) ListRules(ctx context.Context) ([]*ScoreRuleInfo, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/score-rules", nil)
	if err != nil {
		return nil, err
	}
	var rules []*ScoreRuleInfo
	return rules, json.Unmarshal(data, &rules)
}

// DeleteRule deletes a score rule.
func (s *ScoringService) DeleteRule(ctx context.Context, id string) error {
	_, err := s.client.doJSON(ctx, "DELETE", "/v1/score-rules/"+url.PathEscape(id), nil)
	return err
}

// ScoreExperiment triggers offline scoring for an experiment.
func (s *ScoringService) ScoreExperiment(ctx context.Context, experimentID string, ruleIDs []string) error {
	_, err := s.client.doJSON(ctx, "POST", "/v1/experiments/"+url.PathEscape(experimentID)+"/score", map[string]any{
		"rule_ids": ruleIDs,
	})
	return err
}

// GetScores fetches offline scores for an experiment.
func (s *ScoringService) GetScores(ctx context.Context, experimentID string) ([]TraceScoreInfo, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/experiments/"+url.PathEscape(experimentID)+"/scores", nil)
	if err != nil {
		return nil, err
	}
	var scores []TraceScoreInfo
	return scores, json.Unmarshal(data, &scores)
}

// CreateContainsRule is a convenience for creating a "contains" rule.
func (s *ScoringService) CreateContainsRule(ctx context.Context, name, text string) (*ScoreRuleInfo, error) {
	return s.CreateRule(ctx, name, "contains", map[string]any{"text": text})
}

// CreateRegexRule is a convenience for creating a "regex" rule.
func (s *ScoringService) CreateRegexRule(ctx context.Context, name, pattern string) (*ScoreRuleInfo, error) {
	return s.CreateRule(ctx, name, "regex", map[string]any{"pattern": pattern})
}

// CreateLLMJudgeRule is a convenience for creating an "llm_as_judge" rule.
func (s *ScoringService) CreateLLMJudgeRule(ctx context.Context, name, criteria, model string) (*ScoreRuleInfo, error) {
	return s.CreateRule(ctx, name, "llm_as_judge", map[string]any{
		"criteria": criteria,
		"model":    model,
	})
}

func init() {
	_ = fmt.Sprintf // suppress unused import
}
