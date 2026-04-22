package keystone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// SpecService handles spec-related API calls.
type SpecService struct {
	client *Client
}

// Create uploads a new sandbox spec. The body should be raw YAML bytes.
// POST /v1/specs
func (s *SpecService) Create(ctx context.Context, yamlBytes []byte) (*SandboxSpec, error) {
	data, err := s.client.do(ctx, "POST", "/v1/specs", bytes.NewReader(yamlBytes), "application/x-yaml")
	if err != nil {
		return nil, err
	}
	var spec SandboxSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("keystone: decoding spec: %w", err)
	}
	return &spec, nil
}

// Get retrieves a spec by ID.
// GET /v1/specs/:id
func (s *SpecService) Get(ctx context.Context, id string) (*SandboxSpec, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/specs/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var spec SandboxSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("keystone: decoding spec: %w", err)
	}
	return &spec, nil
}

// List returns all specs.
// GET /v1/specs
func (s *SpecService) List(ctx context.Context) ([]*SandboxSpec, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/specs", nil)
	if err != nil {
		return nil, err
	}
	var specs []*SandboxSpec
	if err := json.Unmarshal(data, &specs); err != nil {
		return nil, fmt.Errorf("keystone: decoding specs: %w", err)
	}
	return specs, nil
}

// Delete removes a spec by ID.
// DELETE /v1/specs/:id
func (s *SpecService) Delete(ctx context.Context, id string) error {
	_, err := s.client.doJSON(ctx, "DELETE", "/v1/specs/"+url.PathEscape(id), nil)
	return err
}
