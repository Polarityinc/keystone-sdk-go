package keystone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://keystone.polarity.so"
	defaultTimeout = 30 * time.Second
	apiKeyHeader   = "X-API-Key"
	apiKeyEnvVar   = "KEYSTONE_API_KEY"
)

// Config holds configuration for the Keystone client.
type Config struct {
	// APIKey is sent as the X-API-Key header. If empty, the KEYSTONE_API_KEY
	// environment variable is used.
	APIKey string

	// BaseURL is the base URL of the Keystone server.
	// Default: http://localhost:8012
	BaseURL string

	// Timeout is the HTTP client timeout. Default: 30s.
	Timeout time.Duration
}

// Client is the Keystone API client.
type Client struct {
	Sandboxes   *SandboxService
	Specs       *SpecService
	Experiments *ExperimentService
	Alerts      *AlertService
	Agents      *AgentService
	Datasets    *DatasetService
	Scoring     *ScoringService
	Export      *ExportService
	Prompts     *PromptService

	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// NewClient creates a new Keystone API client with the given configuration.
func NewClient(cfg Config) *Client {
	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv(apiKeyEnvVar)
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	c := &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    baseURL,
		apiKey:     apiKey,
	}

	c.Sandboxes = &SandboxService{client: c}
	c.Specs = &SpecService{client: c}
	c.Experiments = &ExperimentService{client: c}
	c.Alerts = &AlertService{client: c}
	c.Agents = &AgentService{client: c}
	c.Datasets = &DatasetService{client: c}
	c.Scoring = &ScoringService{client: c}
	c.Export = &ExportService{client: c}
	c.Prompts = &PromptService{client: c}

	return c
}

// do executes an HTTP request and returns the response body bytes.
// It returns an *APIError for non-2xx status codes.
func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string) ([]byte, error) {
	url := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("keystone: creating request: %w", err)
	}

	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("keystone: executing request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("keystone: reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		// Try to parse error message from JSON response.
		var errResp struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		if json.Unmarshal(data, &errResp) == nil {
			if errResp.Message != "" {
				apiErr.Message = errResp.Message
			} else if errResp.Error != "" {
				apiErr.Message = errResp.Error
			}
		}
		if apiErr.Message == "" && len(data) > 0 {
			apiErr.Message = string(data)
		}
		return nil, apiErr
	}

	return data, nil
}

// doJSON sends a JSON-encoded request body and returns the raw response bytes.
func (c *Client) doJSON(ctx context.Context, method, path string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("keystone: marshaling request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	return c.do(ctx, method, path, body, "application/json")
}

// FromSandbox creates a client from the environment variables that Keystone
// injects into agent processes (KEYSTONE_BASE_URL, KEYSTONE_API_KEY,
// KEYSTONE_SANDBOX_ID) and returns both the client and the current sandbox
// with its services populated. Use this inside agent code running in a
// Keystone sandbox.
//
//	ks, sb, err := keystone.FromSandbox(ctx)
//	dbInfo := sb.Services["db"]  // host, port, ready
func FromSandbox(ctx context.Context) (*Client, *Sandbox, error) {
	sandboxID := os.Getenv("KEYSTONE_SANDBOX_ID")
	if sandboxID == "" {
		return nil, nil, fmt.Errorf("keystone: KEYSTONE_SANDBOX_ID not set — not running inside a sandbox")
	}
	c := NewClient(Config{
		BaseURL: os.Getenv("KEYSTONE_BASE_URL"),
	})
	sb, err := c.Sandboxes.Get(ctx, sandboxID)
	if err != nil {
		return nil, nil, err
	}
	return c, sb, nil
}

// httpStatusText returns a short status description for a status code.
func httpStatusText(code int) string {
	text := http.StatusText(code)
	if text != "" {
		return strconv.Itoa(code) + " " + text
	}
	return strconv.Itoa(code)
}
