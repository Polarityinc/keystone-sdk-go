package keystone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// SandboxService handles sandbox-related API calls.
type SandboxService struct {
	client *Client
}

// Create creates a new sandbox.
// POST /v1/sandboxes
func (s *SandboxService) Create(ctx context.Context, req CreateSandboxRequest) (*Sandbox, error) {
	data, err := s.client.doJSON(ctx, "POST", "/v1/sandboxes", req)
	if err != nil {
		return nil, err
	}
	var sandbox Sandbox
	if err := json.Unmarshal(data, &sandbox); err != nil {
		return nil, fmt.Errorf("keystone: decoding sandbox: %w", err)
	}
	return &sandbox, nil
}

// Get retrieves a sandbox by ID.
// GET /v1/sandboxes/:id
func (s *SandboxService) Get(ctx context.Context, id string) (*Sandbox, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/sandboxes/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var sandbox Sandbox
	if err := json.Unmarshal(data, &sandbox); err != nil {
		return nil, fmt.Errorf("keystone: decoding sandbox: %w", err)
	}
	return &sandbox, nil
}

// List returns all sandboxes.
// GET /v1/sandboxes
func (s *SandboxService) List(ctx context.Context) ([]*Sandbox, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/sandboxes", nil)
	if err != nil {
		return nil, err
	}
	var sandboxes []*Sandbox
	if err := json.Unmarshal(data, &sandboxes); err != nil {
		return nil, fmt.Errorf("keystone: decoding sandboxes: %w", err)
	}
	return sandboxes, nil
}

// Destroy destroys a sandbox by ID.
// DELETE /v1/sandboxes/:id
func (s *SandboxService) Destroy(ctx context.Context, id string) error {
	_, err := s.client.doJSON(ctx, "DELETE", "/v1/sandboxes/"+url.PathEscape(id), nil)
	return err
}

// RunCommand executes a command inside a sandbox.
// POST /v1/sandboxes/:id/commands
func (s *SandboxService) RunCommand(ctx context.Context, id string, req CommandRequest) (*CommandResult, error) {
	data, err := s.client.doJSON(ctx, "POST", "/v1/sandboxes/"+url.PathEscape(id)+"/commands", req)
	if err != nil {
		return nil, err
	}
	var result CommandResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("keystone: decoding command result: %w", err)
	}
	return &result, nil
}

// ReadFile reads a file from the sandbox.
// GET /v1/sandboxes/:id/files/:path
func (s *SandboxService) ReadFile(ctx context.Context, id string, path string) ([]byte, error) {
	data, err := s.client.do(ctx, "GET", "/v1/sandboxes/"+url.PathEscape(id)+"/files/"+path, nil, "")
	if err != nil {
		return nil, err
	}
	return data, nil
}

// WriteFile writes a file into the sandbox.
// POST /v1/sandboxes/:id/files
func (s *SandboxService) WriteFile(ctx context.Context, id string, path string, content []byte) error {
	req := WriteFileRequest{
		Path:    path,
		Content: string(content),
	}
	b, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("keystone: marshaling write file request: %w", err)
	}
	_, err = s.client.do(ctx, "POST", "/v1/sandboxes/"+url.PathEscape(id)+"/files", bytes.NewReader(b), "application/json")
	return err
}

// DeleteFile deletes a file from the sandbox.
// DELETE /v1/sandboxes/:id/files/:path
func (s *SandboxService) DeleteFile(ctx context.Context, id string, path string) error {
	_, err := s.client.do(ctx, "DELETE", "/v1/sandboxes/"+url.PathEscape(id)+"/files/"+path, nil, "")
	return err
}

// State returns the full state snapshot of a sandbox.
// GET /v1/sandboxes/:id/state
func (s *SandboxService) State(ctx context.Context, id string) (*StateSnapshot, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/sandboxes/"+url.PathEscape(id)+"/state", nil)
	if err != nil {
		return nil, err
	}
	var state StateSnapshot
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("keystone: decoding state snapshot: %w", err)
	}
	return &state, nil
}

// IngestTrace posts tool call trace events to a sandbox. Any agent can use this
// to report what it did — Keystone uses these for scoring, metrics, and observability.
// POST /v1/sandboxes/:id/trace
func (s *SandboxService) IngestTrace(ctx context.Context, id string, events []TraceEvent) error {
	req := map[string]any{"events": events}
	_, err := s.client.doJSON(ctx, "POST", "/v1/sandboxes/"+url.PathEscape(id)+"/trace", req)
	return err
}

// GetTrace returns the trace events and computed metrics for a sandbox.
// GET /v1/sandboxes/:id/trace
func (s *SandboxService) GetTrace(ctx context.Context, id string) (*TraceResponse, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/sandboxes/"+url.PathEscape(id)+"/trace", nil)
	if err != nil {
		return nil, err
	}
	var resp TraceResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("keystone: decoding trace: %w", err)
	}
	return &resp, nil
}

// Diff returns the state diff of a sandbox since creation or last checkpoint.
// GET /v1/sandboxes/:id/diff
func (s *SandboxService) Diff(ctx context.Context, id string) (*StateDiff, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/sandboxes/"+url.PathEscape(id)+"/diff", nil)
	if err != nil {
		return nil, err
	}
	var diff StateDiff
	if err := json.Unmarshal(data, &diff); err != nil {
		return nil, fmt.Errorf("keystone: decoding state diff: %w", err)
	}
	return &diff, nil
}
