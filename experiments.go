package keystone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// ExperimentService handles experiment-related API calls.
type ExperimentService struct {
	client *Client
}

// Create creates a new experiment.
// POST /v1/experiments
func (s *ExperimentService) Create(ctx context.Context, req CreateExperimentRequest) (*Experiment, error) {
	data, err := s.client.doJSON(ctx, "POST", "/v1/experiments", req)
	if err != nil {
		return nil, err
	}
	var exp Experiment
	if err := json.Unmarshal(data, &exp); err != nil {
		return nil, fmt.Errorf("keystone: decoding experiment: %w", err)
	}
	return &exp, nil
}

// Get retrieves an experiment's run results by ID.
// GET /v1/experiments/:id
func (s *ExperimentService) Get(ctx context.Context, id string) (*RunResults, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/experiments/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var results RunResults
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("keystone: decoding run results: %w", err)
	}
	return &results, nil
}

// List returns all experiments.
// GET /v1/experiments
func (s *ExperimentService) List(ctx context.Context) ([]*Experiment, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/experiments", nil)
	if err != nil {
		return nil, err
	}
	var experiments []*Experiment
	if err := json.Unmarshal(data, &experiments); err != nil {
		return nil, fmt.Errorf("keystone: decoding experiments: %w", err)
	}
	return experiments, nil
}

// Run triggers an experiment run.
// POST /v1/experiments/:id/run
func (s *ExperimentService) Run(ctx context.Context, id string) error {
	_, err := s.client.doJSON(ctx, "POST", "/v1/experiments/"+url.PathEscape(id)+"/run", nil)
	return err
}

// Compare compares two experiments.
// POST /v1/experiments/compare
func (s *ExperimentService) Compare(ctx context.Context, baselineID, candidateID string) (*Comparison, error) {
	payload := struct {
		BaselineID  string `json:"baseline_id"`
		CandidateID string `json:"candidate_id"`
	}{
		BaselineID:  baselineID,
		CandidateID: candidateID,
	}
	data, err := s.client.doJSON(ctx, "POST", "/v1/experiments/compare", payload)
	if err != nil {
		return nil, err
	}
	var comparison Comparison
	if err := json.Unmarshal(data, &comparison); err != nil {
		return nil, fmt.Errorf("keystone: decoding comparison: %w", err)
	}
	return &comparison, nil
}

// Metrics retrieves metrics for an experiment.
// GET /v1/metrics/experiments/:id
func (s *ExperimentService) Metrics(ctx context.Context, id string) (*ExperimentMetrics, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/metrics/experiments/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var metrics ExperimentMetrics
	if err := json.Unmarshal(data, &metrics); err != nil {
		return nil, fmt.Errorf("keystone: decoding experiment metrics: %w", err)
	}
	return &metrics, nil
}

// RunAndWaitOpts controls RunAndWait polling + client-side scoring.
type RunAndWaitOpts struct {
	// PollInterval is the time between GET /v1/experiments/:id polls.
	// Default: 2s.
	PollInterval time.Duration
	// Timeout is the maximum wall time to wait for completion.
	// Default: 5 minutes.
	Timeout time.Duration
	// Scores is an optional slice of client-side scorers run against each
	// completed scenario; Scores' InvariantResults are appended to
	// ScenarioResult.Invariants before returning.
	Scores []Scorer
}

// RunAndWait triggers the experiment run, then polls for completion.
//
// When opts.Scores is non-empty, every client-side scorer is evaluated
// against every ScenarioResult after the run completes and the results are
// merged into each scenario's Invariants slice — mirrors Python's
// experiments.run_and_wait(scores=[…]).
func (s *ExperimentService) RunAndWait(ctx context.Context, id string, opts RunAndWaitOpts) (*RunResults, error) {
	pollInterval := opts.PollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	if err := s.Run(ctx, id); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("keystone: experiment %s did not complete within %s", id, timeout)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}

		results, err := s.Get(ctx, id)
		if err != nil {
			// Transient fetch failures don't abort — keep polling.
			if isTransient(err) {
				continue
			}
			return nil, err
		}
		total := results.TotalScenarios
		done := results.Passed + results.Failed + results.Flaky + results.Errors
		if total == 0 || done < total {
			continue
		}

		if len(opts.Scores) > 0 {
			for i := range results.Scenarios {
				scenario := results.Scenarios[i]
				scores := RunClientScorers(ctx, opts.Scores, scenario)
				results.Scenarios[i].Invariants = append(results.Scenarios[i].Invariants, ScoresToInvariantResults(scores)...)
			}
		}
		return results, nil
	}
}

// isTransient returns true for errors we want RunAndWait to keep polling past.
// Currently: network timeouts and 5xx API errors.
func isTransient(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 500 {
		return true
	}
	return false
}
