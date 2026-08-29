package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

const maxHTTPBodyBytes = 8 << 20

type hubClient struct {
	baseURL *url.URL
	nodeID  string
	token   string
	http    *http.Client
}

func (c hubClient) heartbeat(ctx context.Context, request agenthub.HeartbeatRequest) error {
	request.NodeID = c.nodeID
	var response agenthub.HeartbeatResponse
	if err := c.post(ctx, "/api/agent/v1/heartbeat", request, &response); err != nil {
		return err
	}
	if !response.Accepted {
		return errors.New("heartbeat was not accepted")
	}
	return nil
}

func (c hubClient) poll(ctx context.Context) (*agenthub.Task, error) {
	var response agenthub.TaskPollResponse
	if err := c.post(ctx, "/api/agent/v1/tasks/poll", struct{}{}, &response); err != nil {
		return nil, err
	}
	return response.Task, nil
}

func (c hubClient) report(ctx context.Context, taskID int64, request agenthub.TaskResultRequest) error {
	if taskID <= 0 {
		return errors.New("invalid task ID")
	}
	return c.post(ctx, "/api/agent/v1/tasks/"+strconv.FormatInt(taskID, 10)+"/result", request, nil)
}

func (c hubClient) post(ctx context.Context, path string, requestValue, responseValue any) error {
	body, err := json.Marshal(requestValue)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-HServer-Node-ID", c.nodeID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hserver-agent/"+agentVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hub request failed: %w", err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxHTTPBodyBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read hub response: %w", err)
	}
	if len(responseBody) > maxHTTPBodyBytes {
		return errors.New("hub response exceeds the size limit")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("hub returned HTTP %d", resp.StatusCode)
	}
	if responseValue == nil || len(bytes.TrimSpace(responseBody)) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(responseValue); err != nil {
		return fmt.Errorf("decode hub response: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("hub response contains trailing JSON")
	}
	return nil
}
