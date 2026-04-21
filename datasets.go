package keystone

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// DatasetService handles dataset operations.
type DatasetService struct {
	client *Client
}

// Dataset is a versioned collection of input/expected pairs.
type DatasetInfo struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     int    `json:"version"`
	CreatedAt   string `json:"created_at"`
}

// DatasetRecord is a single input/expected pair.
type DatasetRecord struct {
	ID        string         `json:"id,omitempty"`
	DatasetID string         `json:"dataset_id,omitempty"`
	Input     map[string]any `json:"input"`
	Expected  map[string]any `json:"expected,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Tags      []string       `json:"tags,omitempty"`
	Version   int            `json:"version,omitempty"`
}

// Create creates a new dataset.
func (s *DatasetService) Create(ctx context.Context, name, description string) (*DatasetInfo, error) {
	data, err := s.client.doJSON(ctx, "POST", "/v1/datasets", map[string]string{
		"name": name, "description": description,
	})
	if err != nil {
		return nil, err
	}
	var ds DatasetInfo
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("keystone: decoding dataset: %w", err)
	}
	return &ds, nil
}

// List returns all datasets.
func (s *DatasetService) List(ctx context.Context) ([]*DatasetInfo, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/datasets", nil)
	if err != nil {
		return nil, err
	}
	var datasets []*DatasetInfo
	if err := json.Unmarshal(data, &datasets); err != nil {
		return nil, fmt.Errorf("keystone: decoding datasets: %w", err)
	}
	return datasets, nil
}

// Get returns a dataset by ID.
func (s *DatasetService) Get(ctx context.Context, id string) (*DatasetInfo, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/datasets/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var ds DatasetInfo
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("keystone: decoding dataset: %w", err)
	}
	return &ds, nil
}

// Delete deletes a dataset and all its records.
func (s *DatasetService) Delete(ctx context.Context, id string) error {
	_, err := s.client.doJSON(ctx, "DELETE", "/v1/datasets/"+url.PathEscape(id), nil)
	return err
}

// AddRecords adds records to a dataset. Auto-increments the version.
func (s *DatasetService) AddRecords(ctx context.Context, datasetID string, records []DatasetRecord) error {
	_, err := s.client.doJSON(ctx, "POST", "/v1/datasets/"+url.PathEscape(datasetID)+"/records", map[string]any{
		"records": records,
	})
	return err
}

// GetRecords returns records from a dataset, optionally filtered.
func (s *DatasetService) GetRecords(ctx context.Context, datasetID string, opts ...RecordOption) ([]DatasetRecord, error) {
	var o recordOpts
	for _, fn := range opts {
		fn(&o)
	}
	path := "/v1/datasets/" + url.PathEscape(datasetID) + "/records"
	var params []string
	if o.version != nil {
		params = append(params, fmt.Sprintf("version=%d", *o.version))
	}
	if len(o.tags) > 0 {
		params = append(params, "tags="+url.QueryEscape(strings.Join(o.tags, ",")))
	}
	if len(params) > 0 {
		path += "?" + strings.Join(params, "&")
	}
	data, err := s.client.doJSON(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var records []DatasetRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("keystone: decoding records: %w", err)
	}
	return records, nil
}

// RecordOption configures GetRecords filtering.
type RecordOption func(*recordOpts)
type recordOpts struct {
	version *int
	tags    []string
}

// WithRecordVersion filters by dataset version.
func WithRecordVersion(v int) RecordOption { return func(o *recordOpts) { o.version = &v } }

// WithRecordTags filters by tags.
func WithRecordTags(tags ...string) RecordOption { return func(o *recordOpts) { o.tags = tags } }
