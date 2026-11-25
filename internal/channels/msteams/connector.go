package msteams

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	connectorTimeout = 30 * time.Second
	connectorMaxResp = 1 << 20
	maxRetries       = 3
)

// connectorClient is a Bot Framework Connector REST client.
type connectorClient struct {
	httpClient *http.Client
	tokens     *tokenSource
}

func newConnectorClient(ts *tokenSource) *connectorClient {
	return &connectorClient{
		httpClient: &http.Client{Timeout: connectorTimeout},
		tokens:     ts,
	}
}

// postActivity posts an activity to an existing conversation.
func (c *connectorClient) postActivity(ctx context.Context, serviceURL, conversationID string, activity map[string]any) (string, error) {
	url := fmt.Sprintf("%s/v3/conversations/%s/activities",
		strings.TrimRight(serviceURL, "/"), conversationID)
	return c.post(ctx, url, activity)
}

// putActivity replaces an existing activity (for streaming edits).
func (c *connectorClient) putActivity(ctx context.Context, serviceURL, conversationID, activityID string, activity map[string]any) error {
	url := fmt.Sprintf("%s/v3/conversations/%s/activities/%s",
		strings.TrimRight(serviceURL, "/"), conversationID, activityID)
	_, err := c.put(ctx, url, activity)
	return err
}

// createConversation creates a new conversation (for proactive DMs and new channel posts).
func (c *connectorClient) createConversation(ctx context.Context, serviceURL string, params map[string]any) (string, error) {
	url := fmt.Sprintf("%s/v3/conversations", strings.TrimRight(serviceURL, "/"))
	return c.post(ctx, url, params)
}

func (c *connectorClient) post(ctx context.Context, url string, body any) (string, error) {
	return c.doWithRetry(ctx, http.MethodPost, url, body)
}

func (c *connectorClient) put(ctx context.Context, url string, body any) (string, error) {
	return c.doWithRetry(ctx, http.MethodPut, url, body)
}

func (c *connectorClient) doWithRetry(ctx context.Context, method, url string, body any) (string, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		id, err := c.doRequest(ctx, method, url, body)
		if err == nil {
			return id, nil
		}
		lastErr = err
		// Don't retry 4xx (except 429).
		var connErr *connectorError
		if errors.As(err, &connErr) {
			if connErr.StatusCode == http.StatusTooManyRequests {
				// Honor Retry-After header.
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(connErr.RetryAfter):
				}
				continue
			}
			if connErr.StatusCode >= 400 && connErr.StatusCode < 500 {
				return "", err // non-retryable
			}
		}
	}
	return "", lastErr
}

type connectorError struct {
	StatusCode int
	RetryAfter time.Duration
	Body       string
}

func (e *connectorError) Error() string {
	return fmt.Sprintf("connector HTTP %d: %s", e.StatusCode, e.Body)
}

func (c *connectorClient) doRequest(ctx context.Context, method, url string, body any) (string, error) {
	tok, err := c.tokens.Token(ctx, botConnectorScope)
	if err != nil {
		return "", fmt.Errorf("connector: get token: %w", err)
	}

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return "", fmt.Errorf("connector: marshal body: %w", err)
		}
		reqBody = bytes.NewBuffer(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("connector: request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, connectorMaxResp))

	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := 5 * time.Second
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil {
				retryAfter = time.Duration(secs) * time.Second
			}
		}
		return "", &connectorError{StatusCode: resp.StatusCode, RetryAfter: retryAfter, Body: string(respBody)}
	}
	if resp.StatusCode >= 300 {
		return "", &connectorError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var result struct {
		ID string `json:"id"`
	}
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &result)
	}
	return result.ID, nil
}
