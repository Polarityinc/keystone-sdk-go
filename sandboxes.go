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

// SandboxHandle is a bound wrapper around a single sandbox. Returned by
// Create, Get, and Handle. Mirrors the E2B-style "sandbox is the agent's
// tool" mental model — methods like Exec / Read / Write are bound to the
// sandbox ID so the caller doesn't keep typing it.
//
// The embedded *Sandbox makes existing field access (handle.ID, handle.URL,
// handle.Services, etc) work unchanged.
type SandboxHandle struct {
	*Sandbox
	svc *SandboxService
}

// Exec runs a shell command inside the sandbox.
func (h *SandboxHandle) Exec(ctx context.Context, command string) (*CommandResult, error) {
	return h.svc.RunCommand(ctx, h.ID, CommandRequest{Command: command})
}

// ExecWithRequest runs a command with full CommandRequest options (timeout etc).
func (h *SandboxHandle) ExecWithRequest(ctx context.Context, req CommandRequest) (*CommandResult, error) {
	return h.svc.RunCommand(ctx, h.ID, req)
}

// Read reads a file from the workspace.
func (h *SandboxHandle) Read(ctx context.Context, path string) ([]byte, error) {
	return h.svc.ReadFile(ctx, h.ID, path)
}

// Write writes a file to the workspace.
func (h *SandboxHandle) Write(ctx context.Context, path string, content []byte) error {
	return h.svc.WriteFile(ctx, h.ID, path, content)
}

// Delete deletes a file from the workspace.
func (h *SandboxHandle) Delete(ctx context.Context, path string) error {
	return h.svc.DeleteFile(ctx, h.ID, path)
}

// GetState captures the current filesystem state. (Named GetState rather
// than State to avoid shadowing the embedded Sandbox.State string field.)
func (h *SandboxHandle) GetState(ctx context.Context) (*StateSnapshot, error) {
	return h.svc.State(ctx, h.ID)
}

// Diff returns the diff between the baseline snapshot and current state.
func (h *SandboxHandle) Diff(ctx context.Context) (*StateDiff, error) {
	return h.svc.Diff(ctx, h.ID)
}

// Destroy destroys the sandbox and cleans up all resources.
func (h *SandboxHandle) Destroy(ctx context.Context) error {
	return h.svc.Destroy(ctx, h.ID)
}

// IngestTrace posts tool-call trace events from the agent.
func (h *SandboxHandle) IngestTrace(ctx context.Context, events []TraceEvent) error {
	return h.svc.IngestTrace(ctx, h.ID, events)
}

// GetTrace fetches trace events + computed metrics.
func (h *SandboxHandle) GetTrace(ctx context.Context) (*TraceResponse, error) {
	return h.svc.GetTrace(ctx, h.ID)
}

// Refresh re-fetches metadata from the server (state may have changed).
func (h *SandboxHandle) Refresh(ctx context.Context) error {
	fresh, err := h.svc.GetInfo(ctx, h.ID)
	if err != nil {
		return err
	}
	h.Sandbox = fresh
	return nil
}

// Create creates a new sandbox. Returns a bound handle.
// POST /v1/sandboxes
func (s *SandboxService) Create(ctx context.Context, req CreateSandboxRequest) (*SandboxHandle, error) {
	info, err := s.create(ctx, req)
	if err != nil {
		return nil, err
	}
	return &SandboxHandle{Sandbox: info, svc: s}, nil
}

func (s *SandboxService) create(ctx context.Context, req CreateSandboxRequest) (*Sandbox, error) {
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

// Get retrieves a sandbox by ID. Returns a bound handle.
// GET /v1/sandboxes/:id
func (s *SandboxService) Get(ctx context.Context, id string) (*SandboxHandle, error) {
	info, err := s.GetInfo(ctx, id)
	if err != nil {
		return nil, err
	}
	return &SandboxHandle{Sandbox: info, svc: s}, nil
}

// Handle builds a bound handle from an ID without making a network call.
func (s *SandboxService) Handle(id string) *SandboxHandle {
	return &SandboxHandle{Sandbox: &Sandbox{ID: id}, svc: s}
}

// GetInfo returns the raw Sandbox metadata for an ID without wrapping.
func (s *SandboxService) GetInfo(ctx context.Context, id string) (*Sandbox, error) {
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

// List returns all sandboxes as bound handles.
// GET /v1/sandboxes
func (s *SandboxService) List(ctx context.Context) ([]*SandboxHandle, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/sandboxes", nil)
	if err != nil {
		return nil, err
	}
	var sandboxes []*Sandbox
	if err := json.Unmarshal(data, &sandboxes); err != nil {
		return nil, fmt.Errorf("keystone: decoding sandboxes: %w", err)
	}
	handles := make([]*SandboxHandle, len(sandboxes))
	for i, sb := range sandboxes {
		handles[i] = &SandboxHandle{Sandbox: sb, svc: s}
	}
	return handles, nil
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
