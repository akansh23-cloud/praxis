/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The Prometheus collector: the LLD's RED/USE + SLO-burn numbers, from a
// SMALL, CODE-OWNED query-template set. There is no path by which any
// model, config field or annotation supplies PromQL here — the templates
// below are the complete vocabulary, parameterized only by the incident's
// scope namespaces. Telemetry that comes back is DATA: it is recorded
// verbatim into Metric items and never interpreted as instructions.
//
// Honesty over completeness: a failed query becomes an item carrying the
// error, an empty result becomes an item with an empty series list, and a
// cluster with no Prometheus configured simply contributes no Metric items
// — nothing is fabricated to look like a measurement.

// QueryClient is the seam to Prometheus's instant-query API. The
// production implementation is HTTPQueryClient; tests use fakes.
type QueryClient interface {
	Query(ctx context.Context, promql string) ([]Sample, error)
}

// Sample is one series of an instant-query result. Value keeps the API's
// own decimal string — never a float round-trip — so rendering is
// deterministic for a given response.
type Sample struct {
	Labels map[string]string
	Value  string
}

// QueryTemplate is one fixed, code-owned query. Expr carries exactly one
// %s, filled with the anchored scope-namespace regex.
type QueryTemplate struct {
	ID     string // Metric item natural key
	Signal string // RED | USE | SLO
	Expr   string
}

// Signal classes.
const (
	SignalRED = "RED"
	SignalUSE = "USE"
	SignalSLO = "SLO"
)

// Metric item data keys. An item carries either MetricDataSeries (the
// query succeeded; possibly empty) or MetricDataError (it did not) —
// never both, so readers can tell measurement from unavailability by key.
const (
	MetricDataTemplate = "template"
	MetricDataSignal   = "signal"
	MetricDataPromQL   = "promql"
	MetricDataSeries   = "series"
	MetricDataError    = "error"
)

// queryTemplates is the ENTIRE Prometheus vocabulary of the evidence
// path (LLD §6 / master plan Phase 3: "RED/USE + SLO burn ... from a
// small template set"). The RED pair reads the error side of
// rate/errors/duration from what every kube-state-metrics install
// exports; the USE trio covers utilization and saturation via cadvisor;
// the SLO template reads the conventional burn-rate recording rule and
// honestly comes back empty where no such rule is defined.
var queryTemplates = []QueryTemplate{
	{ID: "red-restarts", Signal: SignalRED,
		Expr: `sum by (namespace, pod) (increase(kube_pod_container_status_restarts_total{namespace=~"%s"}[15m])) > 0`},
	{ID: "red-unready-pods", Signal: SignalRED,
		Expr: `sum by (namespace, pod) (kube_pod_status_ready{namespace=~"%s",condition="false"}) > 0`},
	{ID: "use-cpu-usage", Signal: SignalUSE,
		Expr: `sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{namespace=~"%s",container!=""}[5m]))`},
	{ID: "use-memory-working-set", Signal: SignalUSE,
		Expr: `sum by (namespace, pod) (container_memory_working_set_bytes{namespace=~"%s",container!=""})`},
	{ID: "use-cpu-throttling", Signal: SignalUSE,
		Expr: `sum by (namespace, pod) (rate(container_cpu_cfs_throttled_periods_total{namespace=~"%s"}[5m])) > 0`},
	{ID: "slo-error-budget-burn", Signal: SignalSLO,
		Expr: `max by (namespace) (slo:error_budget_burn_rate{namespace=~"%s"})`},
}

// CollectPrometheus runs every template once against the scope and returns
// one Metric item per template. A nil client means "no Prometheus
// configured": no items, and the caller records the omission where humans
// look (the incident's condition message).
func CollectPrometheus(ctx context.Context, qc QueryClient, namespaces []string) []Collected {
	if qc == nil {
		return nil
	}
	nsRegex := scopeRegex(namespaces)
	out := make([]Collected, 0, len(queryTemplates))
	for _, tmpl := range queryTemplates {
		promql := fmt.Sprintf(tmpl.Expr, nsRegex)
		data := map[string]string{
			MetricDataTemplate: tmpl.ID,
			MetricDataSignal:   tmpl.Signal,
			MetricDataPromQL:   promql,
		}
		if samples, err := qc.Query(ctx, promql); err != nil {
			data[MetricDataError] = err.Error()
		} else {
			data[MetricDataSeries] = renderSeries(samples)
		}
		out = append(out, Collected{
			Type:   ItemTypeMetric,
			Source: SourcePrometheus,
			Key:    tmpl.ID,
			Data:   data,
		})
	}
	return out
}

// scopeRegex renders the namespaces as one anchored, escaped alternation,
// sorted so any ordering of the same scope instantiates the same PromQL.
func scopeRegex(namespaces []string) string {
	escaped := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		escaped = append(escaped, regexp.QuoteMeta(ns))
	}
	slices.Sort(escaped)
	return "^(?:" + strings.Join(escaped, "|") + ")$"
}

// renderSeries flattens samples into sorted "{label=...} value" lines —
// one deterministic string per query for a given response.
func renderSeries(samples []Sample) string {
	lines := make([]string, 0, len(samples))
	for _, s := range samples {
		keys := make([]string, 0, len(s.Labels))
		for k := range s.Labels {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		pairs := make([]string, 0, len(keys))
		for _, k := range keys {
			pairs = append(pairs, fmt.Sprintf("%s=%q", k, s.Labels[k]))
		}
		lines = append(lines, fmt.Sprintf("{%s} %s", strings.Join(pairs, ","), s.Value))
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// HTTPQueryClient is the production QueryClient: instant queries against
// /api/v1/query, values kept as the API's own strings.
type HTTPQueryClient struct {
	base string
	hc   *http.Client
}

// NewHTTPQueryClient builds a client for the given Prometheus base URL
// (e.g. http://prometheus.monitoring:9090).
func NewHTTPQueryClient(baseURL string) (*HTTPQueryClient, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("prometheus URL %q is not an absolute URL", baseURL)
	}
	return &HTTPQueryClient{
		base: strings.TrimRight(baseURL, "/"),
		hc:   &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// promResponse is the /api/v1/query wire shape this client consumes.
type promResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []json.RawMessage `json:"value"` // [unix_ts, "value"]
		} `json:"result"`
	} `json:"data"`
}

func (c *HTTPQueryClient) Query(ctx context.Context, promql string) ([]Sample, error) {
	endpoint := c.base + "/api/v1/query?query=" + url.QueryEscape(promql)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build prometheus request: %w", err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query prometheus: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read prometheus response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus returned HTTP %d: %.200s", resp.StatusCode, body)
	}
	var pr promResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, fmt.Errorf("parse prometheus response: %w", err)
	}
	if pr.Status != "success" {
		return nil, fmt.Errorf("prometheus query failed: %s", pr.Error)
	}
	if pr.Data.ResultType != "vector" {
		return nil, fmt.Errorf("unexpected result type %q (instant queries return vectors)", pr.Data.ResultType)
	}

	samples := make([]Sample, 0, len(pr.Data.Result))
	for _, r := range pr.Data.Result {
		if len(r.Value) != 2 {
			return nil, fmt.Errorf("malformed sample value %v", r.Value)
		}
		var value string
		if err := json.Unmarshal(r.Value[1], &value); err != nil {
			return nil, fmt.Errorf("malformed sample value: %w", err)
		}
		samples = append(samples, Sample{Labels: r.Metric, Value: value})
	}
	return samples, nil
}
