/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package evidence

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/akansh23-cloud/praxis/internal/evidence/logs"
)

// The Loki collector: LogTemplate evidence for the incident's scope
// (LLD §6, FR-P3-03), from ONE code-owned LogQL selector per scope
// namespace over a fixed, bounded window. There is no path by which any
// model, config field, annotation or log line supplies LogQL here: the
// selector below is the complete vocabulary, parameterized only by a
// scope namespace that must be a DNS label.
//
// Raw lines never cross into evidence. They exist here only long enough
// to be normalized, grouped by container and clustered (ADR-007); what
// leaves is one item per (container, template) carrying the template, the
// line count, the contributing pods and exactly one exemplar line. The
// assembler then scrubs every value (redact.go) before canonical bytes
// exist.
//
// Honesty over completeness, as for Prometheus: a failed query becomes an
// item carrying the error and the query; an empty window contributes no
// items and says so in the notes; no Loki configured contributes nothing.

const (
	// LogWindow is how far back from the collection instant lines are
	// read — the same horizon as the red-restarts metric template.
	LogWindow = 15 * time.Minute

	// MaxLogLines bounds one range query (newest lines first). It equals
	// Loki's default max_entries_limit_per_query, so the bound is honoured
	// by the server too.
	MaxLogLines = 5000

	// MaxLogTemplates bounds how many templates cross into the bundle per
	// collection: the most frequent ones. LogTemplate is a low-retention
	// type (ADR-006), so a flood of templates would otherwise evict owner
	// chains and then itself under the 64-item cap.
	MaxLogTemplates = 16

	// maxLogPods bounds how many contributing pod names an item lists.
	maxLogPods = 8
)

// LogTemplate item data keys. A template item carries container, pods,
// template, count and exemplar; an error item carries logql and error —
// never both shapes at once.
const (
	LogDataContainer = "container"
	LogDataPods      = "pods"
	LogDataTemplate  = "template"
	LogDataCount     = "count"
	LogDataExemplar  = "exemplar"
	LogDataLogQL     = "logql"
	LogDataError     = "error"
)

// Stream labels the loki-stack promtail attaches to pod logs.
const (
	logLabelContainer = "container"
	logLabelPod       = "pod"
)

// dnsLabel is the RFC 1123 label shape every Kubernetes namespace has.
// A scope entry that fails it names no namespace and is never
// interpolated into a query.
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// logQuery renders the ONLY LogQL this path sends.
func logQuery(namespace string) string {
	return `{namespace="` + namespace + `"}`
}

// logGroup accumulates one (namespace, container)'s lines and pods.
type logGroup struct {
	namespace string
	container string
	pods      map[string]bool
	lines     []string
}

// CollectLogs queries each scope namespace once over [now-LogWindow, now]
// and returns LogTemplate items plus human-readable notes for the
// incident's condition message. A nil client means no Loki is configured:
// nothing is returned and the caller records the omission.
func CollectLogs(ctx context.Context, lc logs.Client, namespaces []string, now time.Time) ([]Collected, []string) {
	if lc == nil {
		return nil, nil
	}
	nss := slices.Clone(namespaces)
	slices.Sort(nss)
	start, end := now.Add(-LogWindow), now

	var out []Collected
	groups := map[string]*logGroup{}
	totalLines := 0
	for _, ns := range nss {
		if !dnsLabel.MatchString(ns) {
			out = append(out, logErrorItem(ns, "",
				fmt.Sprintf("scope namespace %q is not a DNS label; logs not queried", ns)))
			continue
		}
		q := logQuery(ns)
		streams, err := lc.QueryRange(ctx, q, start, end, MaxLogLines)
		if err != nil {
			out = append(out, logErrorItem(ns, q, err.Error()))
			continue
		}
		for i := range streams {
			s := &streams[i]
			container := orDefault(s.Labels[logLabelContainer], "unknown")
			key := ns + "/" + container
			g := groups[key]
			if g == nil {
				g = &logGroup{namespace: ns, container: container, pods: map[string]bool{}}
				groups[key] = g
			}
			if pod := s.Labels[logLabelPod]; pod != "" {
				g.pods[pod] = true
			}
			for _, raw := range s.Lines {
				if line := logs.Normalize(raw); line != "" {
					g.lines = append(g.lines, line)
					totalLines++
				}
			}
		}
	}

	var items []Collected
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		g := groups[key]
		pods := renderPods(slices.Sorted(maps.Keys(g.pods)))
		for _, c := range logs.Templatize(g.lines) {
			items = append(items, Collected{
				Type:   ItemTypeLogTemplate,
				Source: SourceLoki,
				Key:    g.namespace + "/" + g.container + "/" + c.Template,
				Data: map[string]string{
					DataNamespace:    g.namespace,
					LogDataContainer: g.container,
					LogDataPods:      pods,
					LogDataTemplate:  c.Template,
					LogDataCount:     strconv.Itoa(c.Count),
					LogDataExemplar:  c.Exemplar,
				},
			})
		}
	}

	notes := []string{fmt.Sprintf("loki: %d lines in the last %s → %d templates", totalLines, LogWindow, len(items))}
	if len(items) > MaxLogTemplates {
		slices.SortFunc(items, func(a, b Collected) int {
			return cmp.Or(
				cmp.Compare(templateCount(b), templateCount(a)),
				cmp.Compare(a.Key, b.Key),
			)
		})
		notes = append(notes, fmt.Sprintf("loki: kept the %d most frequent of %d templates", MaxLogTemplates, len(items)))
		items = items[:MaxLogTemplates]
	}
	return append(out, items...), notes
}

func logErrorItem(namespace, logql, message string) Collected {
	data := map[string]string{
		DataNamespace: namespace,
		LogDataError:  message,
	}
	if logql != "" {
		data[LogDataLogQL] = logql
	}
	return Collected{
		Type:   ItemTypeLogTemplate,
		Source: SourceLoki,
		Key:    namespace + "/error",
		Data:   data,
	}
}

func templateCount(c Collected) int {
	n, _ := strconv.Atoi(c.Data[LogDataCount])
	return n
}

// renderPods lists the contributing pods, sorted, bounded at maxLogPods.
func renderPods(pods []string) string {
	if len(pods) <= maxLogPods {
		return strings.Join(pods, ",")
	}
	return strings.Join(pods[:maxLogPods], ",") + fmt.Sprintf(" +%d more", len(pods)-maxLogPods)
}
