/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeProm answers instant queries from a canned table keyed by the
// instantiated PromQL. Unknown queries error — the test then also proves
// the collector instantiated exactly the expressions it claims to own.
type fakeProm struct {
	responses map[string][]Sample
	errors    map[string]error
	queries   []string
}

func (f *fakeProm) Query(_ context.Context, promql string) ([]Sample, error) {
	f.queries = append(f.queries, promql)
	if err, ok := f.errors[promql]; ok {
		return nil, err
	}
	if samples, ok := f.responses[promql]; ok {
		return samples, nil
	}
	return nil, fmt.Errorf("unexpected query %q", promql)
}

const scopeShopRegex = `^(?:shop)$`

func instantiated(id string) string {
	for _, tmpl := range queryTemplates {
		if tmpl.ID == id {
			return fmt.Sprintf(tmpl.Expr, scopeShopRegex)
		}
	}
	panic("unknown template " + id)
}

func promFixture() *fakeProm {
	f := &fakeProm{responses: map[string][]Sample{}, errors: map[string]error{}}
	// use-memory: two series handed back deliberately unsorted, one value
	// carrying float noise that must survive verbatim.
	f.responses[instantiated(tmplUseMemoryFull)] = []Sample{
		{Labels: map[string]string{keyNamespace: goldenNS, OwnerDataPod: "storefront-1"}, Value: "0.30000000000000004"},
		{Labels: map[string]string{keyNamespace: goldenNS, OwnerDataPod: goldenPod}, Value: "41943040"},
	}
	// restarts: empty result — a healthy namespace, honestly empty.
	f.responses[instantiated("red-restarts")] = nil
	// slo burn: no recording rule in this cluster → query error.
	f.errors[instantiated("slo-error-budget-burn")] = fmt.Errorf("query prometheus: connection refused")
	// remaining templates: one series each.
	for _, id := range []string{"red-unready-pods", "use-cpu-usage", "use-cpu-throttling"} {
		f.responses[instantiated(id)] = []Sample{
			{Labels: map[string]string{keyNamespace: goldenNS, OwnerDataPod: goldenPod}, Value: "1"},
		}
	}
	return f
}

const tmplUseMemoryFull = "use-memory-working-set"

func metricByKey(t *testing.T, items []Collected, key string) Collected {
	t.Helper()
	for _, it := range items {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("no Metric item with key %q", key)
	return Collected{}
}

func TestCollectPrometheusTemplates(t *testing.T) {
	f := promFixture()
	items := CollectPrometheus(context.Background(), f, []string{goldenNS})

	if len(items) != len(queryTemplates) {
		t.Fatalf("got %d Metric items, want one per template (%d)", len(items), len(queryTemplates))
	}
	for _, it := range items {
		if it.Type != ItemTypeMetric || it.Source != SourcePrometheus {
			t.Errorf("item %q has type/source %s/%s", it.Key, it.Type, it.Source)
		}
		if it.Data[MetricDataPromQL] == "" || it.Data[MetricDataSignal] == "" {
			t.Errorf("item %q is missing promql/signal: %v", it.Key, it.Data)
		}
	}

	// Success: series rendered sorted, values verbatim.
	mem := metricByKey(t, items, tmplUseMemoryFull)
	wantSeries := `{namespace="shop",pod="checkout-api-7d9c6f5b4-x2m8q"} 41943040` + "\n" +
		`{namespace="shop",pod="storefront-1"} 0.30000000000000004`
	if got := mem.Data[MetricDataSeries]; got != wantSeries {
		t.Errorf("series rendering:\n got %q\nwant %q", got, wantSeries)
	}
	if _, hasErr := mem.Data[MetricDataError]; hasErr {
		t.Error("successful query carries an error key")
	}

	// Empty result: series key present and empty; not an error.
	restarts := metricByKey(t, items, "red-restarts")
	if got, ok := restarts.Data[MetricDataSeries]; !ok || got != "" {
		t.Errorf("empty result must yield an empty series value, got %v", restarts.Data)
	}

	// Failed query: error recorded honestly, no fabricated series.
	burn := metricByKey(t, items, "slo-error-budget-burn")
	if got := burn.Data[MetricDataError]; !strings.Contains(got, "connection refused") {
		t.Errorf("error item data = %v", burn.Data)
	}
	if _, hasSeries := burn.Data[MetricDataSeries]; hasSeries {
		t.Error("failed query fabricated a series key")
	}
}

// TestCollectPrometheusDeterministic: same fake responses in a different
// sample order produce identical items — series rendering sorts.
func TestCollectPrometheusDeterministic(t *testing.T) {
	forward := CollectPrometheus(context.Background(), promFixture(), []string{goldenNS})

	reversed := promFixture()
	s := reversed.responses[instantiated(tmplUseMemoryFull)]
	s[0], s[1] = s[1], s[0]
	backward := CollectPrometheus(context.Background(), reversed, []string{goldenNS})

	a := metricByKey(t, forward, tmplUseMemoryFull)
	b := metricByKey(t, backward, tmplUseMemoryFull)
	if a.Data[MetricDataSeries] != b.Data[MetricDataSeries] {
		t.Errorf("sample order leaked into the rendered series:\n%q\n%q", a.Data[MetricDataSeries], b.Data[MetricDataSeries])
	}
}

// TestScopeRegex: sorted, escaped, anchored — the same scope always
// instantiates the same PromQL, and a namespace name cannot smuggle
// regex syntax into a query.
func TestScopeRegex(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{goldenNS}, scopeShopRegex},
		{[]string{"b", "a"}, `^(?:a|b)$`},
		{[]string{"a", "b"}, `^(?:a|b)$`},
		{[]string{"team.x"}, `^(?:team\.x)$`},
	}
	for _, tc := range cases {
		if got := scopeRegex(tc.in); got != tc.want {
			t.Errorf("scopeRegex(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCollectPrometheusNilClient(t *testing.T) {
	if items := CollectPrometheus(context.Background(), nil, []string{goldenNS}); items != nil {
		t.Errorf("nil client must contribute no items, got %v", items)
	}
}

func TestHTTPQueryClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("query") {
		case "vector_ok":
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"pod":"a"},"value":[1756728000,"0.30000000000000004"]}]}}`)
		case "prom_error":
			_, _ = fmt.Fprint(w, `{"status":"error","error":"bad expression"}`)
		case "not_vector":
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, "boom")
		}
	}))
	defer srv.Close()

	qc, err := NewHTTPQueryClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	samples, err := qc.Query(ctx, "vector_ok")
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || samples[0].Value != "0.30000000000000004" || samples[0].Labels["pod"] != "a" {
		t.Errorf("parsed samples = %+v — the value must survive verbatim", samples)
	}

	for _, q := range []string{"prom_error", "not_vector", "http_500"} {
		if _, err := qc.Query(ctx, q); err == nil {
			t.Errorf("query %q must error", q)
		}
	}

	if _, err := NewHTTPQueryClient("not a url"); err == nil {
		t.Error("NewHTTPQueryClient accepted a relative garbage URL")
	}
}
