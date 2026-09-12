/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Stream is one Loki stream of a range query: its label set and its lines
// (timestamps are dropped — templating does not use them, and the fewer
// raw facts travel, the better).
type Stream struct {
	Labels map[string]string
	Lines  []string
}

// Client is the seam to Loki's range-query API. The production
// implementation is HTTPClient; tests use fakes. Callers own the LogQL —
// the evidence collector instantiates it from code-owned templates only.
type Client interface {
	QueryRange(ctx context.Context, logql string, start, end time.Time, limit int) ([]Stream, error)
}

// HTTPClient is the production Client: GET /loki/api/v1/query_range,
// newest lines first, bounded by the caller's limit.
type HTTPClient struct {
	base string
	hc   *http.Client
}

// NewHTTPClient builds a client for the given Loki base URL (e.g.
// http://loki.monitoring:3100).
func NewHTTPClient(baseURL string) (*HTTPClient, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("loki URL %q is not an absolute URL", baseURL)
	}
	return &HTTPClient{
		base: strings.TrimRight(baseURL, "/"),
		hc:   &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// maxResponseBytes bounds how much of a Loki response is read: the
// caller's line limit times MaxLineBytes, with generous room for labels
// and JSON framing, but never unbounded.
const maxResponseBytes = 16 << 20

// lokiResponse is the /loki/api/v1/query_range wire shape this client
// consumes for log (stream) queries.
type lokiResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"` // [ns-timestamp, line]
		} `json:"result"`
	} `json:"data"`
}

// QueryRange implements Client.
func (c *HTTPClient) QueryRange(ctx context.Context, logql string, start, end time.Time, limit int) ([]Stream, error) {
	q := url.Values{}
	q.Set("query", logql)
	q.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	q.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	q.Set("limit", strconv.Itoa(limit))
	q.Set("direction", "backward")
	endpoint := c.base + "/loki/api/v1/query_range?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build loki request: %w", err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query loki: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read loki response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("loki returned HTTP %d: %.200s", resp.StatusCode, body)
	}
	var lr lokiResponse
	if err := json.Unmarshal(body, &lr); err != nil {
		return nil, fmt.Errorf("parse loki response: %w", err)
	}
	if lr.Status != "success" {
		return nil, fmt.Errorf("loki query failed: %s", lr.Error)
	}
	if lr.Data.ResultType != "streams" {
		return nil, fmt.Errorf("unexpected result type %q (log queries return streams)", lr.Data.ResultType)
	}

	streams := make([]Stream, 0, len(lr.Data.Result))
	for _, r := range lr.Data.Result {
		s := Stream{Labels: r.Stream, Lines: make([]string, 0, len(r.Values))}
		for _, v := range r.Values {
			if len(v) != 2 {
				return nil, fmt.Errorf("malformed stream entry with %d fields", len(v))
			}
			s.Lines = append(s.Lines, v[1])
		}
		streams = append(streams, s)
	}
	return streams, nil
}
