package keystone

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// AlertService handles alert-related API calls.
type AlertService struct {
	client *Client
}

// Create creates a new alert.
// POST /v1/alerts
func (s *AlertService) Create(ctx context.Context, req CreateAlertRequest) (*Alert, error) {
	data, err := s.client.doJSON(ctx, "POST", "/v1/alerts", req)
	if err != nil {
		return nil, err
	}
	var alert Alert
	if err := json.Unmarshal(data, &alert); err != nil {
		return nil, fmt.Errorf("keystone: decoding alert: %w", err)
	}
	return &alert, nil
}

// Get retrieves an alert by ID.
// GET /v1/alerts/:id
func (s *AlertService) Get(ctx context.Context, id string) (*Alert, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/alerts/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var alert Alert
	if err := json.Unmarshal(data, &alert); err != nil {
		return nil, fmt.Errorf("keystone: decoding alert: %w", err)
	}
	return &alert, nil
}

// List returns all alerts.
// GET /v1/alerts
func (s *AlertService) List(ctx context.Context) ([]*Alert, error) {
	data, err := s.client.doJSON(ctx, "GET", "/v1/alerts", nil)
	if err != nil {
		return nil, err
	}
	var alerts []*Alert
	if err := json.Unmarshal(data, &alerts); err != nil {
		return nil, fmt.Errorf("keystone: decoding alerts: %w", err)
	}
	return alerts, nil
}

// Delete removes an alert by ID.
// DELETE /v1/alerts/:id
func (s *AlertService) Delete(ctx context.Context, id string) error {
	_, err := s.client.doJSON(ctx, "DELETE", "/v1/alerts/"+url.PathEscape(id), nil)
	return err
}
