package keystone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
)

// AgentService handles agent snapshot operations.
type AgentService struct {
	client *Client
}

// Upload uploads an agent snapshot. The bundle is a tarball (io.Reader).
// Version is auto-assigned by the server.
func (s *AgentService) Upload(ctx context.Context, req UploadSnapshotRequest, bundle io.Reader) (*AgentSnapshot, error) {
	// Build multipart form: "metadata" (JSON) + "bundle" (tarball).
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	metaJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("keystone: marshaling metadata: %w", err)
	}
	if err := mw.WriteField("metadata", string(metaJSON)); err != nil {
		return nil, fmt.Errorf("keystone: writing metadata field: %w", err)
	}

	fw, err := mw.CreateFormFile("bundle", req.Name+".tar.gz")
	if err != nil {
		return nil, fmt.Errorf("keystone: creating bundle field: %w", err)
	}
	if _, err := io.Copy(fw, bundle); err != nil {
		return nil, fmt.Errorf("keystone: copying bundle: %w", err)
	}
	mw.Close()

	data, err := s.client.do(ctx, "POST", "/v1/agents", &buf, mw.FormDataContentType())
	if err != nil {
		return nil, err
	}
	var snap AgentSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("keystone: decoding snapshot: %w", err)
	}
	return &snap, nil
}

// GetOption configures how Get resolves a snapshot.
type GetOption func(*getOpts)
type getOpts struct {
	version *int
	tag     string
}

// WithVersion resolves a specific version number.
func WithVersion(v int) GetOption { return func(o *getOpts) { o.version = &v } }

// WithTag resolves by tag label.
func WithTag(t string) GetOption { return func(o *getOpts) { o.tag = t } }

// Get resolves a snapshot by name. Without options, returns the latest version.
func (s *AgentService) Get(ctx context.Context, name string, opts ...GetOption) (*AgentSnapshot, error) {
	var o getOpts
	for _, fn := range opts {
		fn(&o)
	}

	var path string
	switch {
	case o.tag != "":
		path = "/v1/agents/" + url.PathEscape(name) + "/tags/" + url.PathEscape(o.tag)
	case o.version != nil:
		path = fmt.Sprintf("/v1/agents/%s/versions/%d", url.PathEscape(name), *o.version)
	default:
		path = "/v1/agents/" + url.PathEscape(name) + "/latest"
	}

	data, err := s.client.doJSON(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var snap AgentSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("keystone: decoding snapshot: %w", err)
	}
	return &snap, nil
}

// GetByID returns a snapshot by its immutable content-addressed ID.
func (s *AgentService) GetByID(ctx context.Context, id string) (*AgentSnapshot, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/snapshots/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var snap AgentSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("keystone: decoding snapshot: %w", err)
	}
	return &snap, nil
}

// ListOption configures pagination for list operations.
type ListOption func(*listOpts)
type listOpts struct {
	limit  int
	cursor string
}

// WithLimit sets the page size.
func WithLimit(n int) ListOption { return func(o *listOpts) { o.limit = n } }

// WithCursor sets the pagination cursor.
func WithCursor(c string) ListOption { return func(o *listOpts) { o.cursor = c } }

// List returns all agent snapshots with cursor pagination.
func (s *AgentService) List(ctx context.Context, opts ...ListOption) (*AgentPage, error) {
	o := listOpts{limit: 100}
	for _, fn := range opts {
		fn(&o)
	}
	path := fmt.Sprintf("/v1/agents?limit=%d", o.limit)
	if o.cursor != "" {
		path += "&cursor=" + url.QueryEscape(o.cursor)
	}
	data, err := s.client.doJSON(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var page AgentPage
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("keystone: decoding page: %w", err)
	}
	return &page, nil
}

// ListVersions returns all versions of a named agent with cursor pagination.
func (s *AgentService) ListVersions(ctx context.Context, name string, opts ...ListOption) (*AgentPage, error) {
	o := listOpts{limit: 100}
	for _, fn := range opts {
		fn(&o)
	}
	path := fmt.Sprintf("/v1/agents/%s/versions?limit=%d", url.PathEscape(name), o.limit)
	if o.cursor != "" {
		path += "&cursor=" + url.QueryEscape(o.cursor)
	}
	data, err := s.client.doJSON(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var page AgentPage
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("keystone: decoding page: %w", err)
	}
	return &page, nil
}

// Delete removes a snapshot. Pass the AgentSnapshot object, not raw strings.
func (s *AgentService) Delete(ctx context.Context, snapshot *AgentSnapshot) error {
	_, err := s.client.doJSON(ctx, "DELETE", "/v1/snapshots/"+url.PathEscape(snapshot.ID), nil)
	return err
}
