/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package logs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// TestHTTPClientQueryRange pins the wire contract: the exact endpoint and
// parameters sent (query verbatim, nanosecond bounds, limit, newest-first),
// the parsed streams, and honest errors for every failure shape.
func TestHTTPClientQueryRange(t *testing.T) {
	var seen *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		switch r.URL.Query().Get("query") {
		case `{namespace="shop"}`:
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"streams","result":[
				{"stream":{"namespace":"shop","container":"checkout-api","pod":"checkout-api-1"},
				 "values":[["1757699140000000000","second line"],["1757699139000000000","first line"]]},
				{"stream":{"namespace":"shop","container":"log-noise","pod":"checkout-api-1"},
				 "values":[["1757699140000000000","noise"]]}]}}`)
		case "loki_error":
			_, _ = fmt.Fprint(w, `{"status":"error","error":"parse error at line 1"}`)
		case "not_streams":
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
		case "malformed_entry":
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"streams","result":[{"stream":{},"values":[["only-one-field"]]}]}}`)
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, "boom")
		}
	}))
	defer srv.Close()

	lc, err := NewHTTPClient(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	end := time.Date(2026, 9, 12, 17, 45, 40, 0, time.UTC)
	start := end.Add(-15 * time.Minute)

	streams, err := lc.QueryRange(ctx, `{namespace="shop"}`, start, end, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if seen.URL.Path != "/loki/api/v1/query_range" {
		t.Errorf("path = %q", seen.URL.Path)
	}
	params := seen.URL.Query()
	if got := params.Get("start"); got != strconv.FormatInt(start.UnixNano(), 10) {
		t.Errorf("start = %q, want nanoseconds of %v", got, start)
	}
	if got := params.Get("end"); got != strconv.FormatInt(end.UnixNano(), 10) {
		t.Errorf("end = %q, want nanoseconds of %v", got, end)
	}
	if params.Get("limit") != "5000" || params.Get("direction") != "backward" {
		t.Errorf("limit/direction = %q/%q", params.Get("limit"), params.Get("direction"))
	}
	if len(streams) != 2 {
		t.Fatalf("got %d streams, want 2", len(streams))
	}
	if streams[0].Labels["container"] != "checkout-api" || len(streams[0].Lines) != 2 ||
		streams[0].Lines[0] != "second line" || streams[0].Lines[1] != "first line" {
		t.Errorf("stream 0 = %+v — lines must survive verbatim in response order", streams[0])
	}
	if streams[1].Labels["container"] != "log-noise" || len(streams[1].Lines) != 1 {
		t.Errorf("stream 1 = %+v", streams[1])
	}

	for _, q := range []string{"loki_error", "not_streams", "malformed_entry", "http_500"} {
		if _, err := lc.QueryRange(ctx, q, start, end, 10); err == nil {
			t.Errorf("query %q must error", q)
		}
	}
	if _, err := NewHTTPClient("not a url"); err == nil {
		t.Error("NewHTTPClient accepted a relative garbage URL")
	}
}
