/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package scoring is the referee (playbook Session 2.3): it turns what a
// run observed into the deterministic per-run metrics of LLD §17.3 and
// FR-P2-03, serializes them as JSONL, and aggregates mean/min/max
// distributions over N runs (FR-P2-05). Everything here is exact string
// and set arithmetic — no fuzzy matching, no NLP similarity, no model in
// the loop. The benchmark is the referee; the referee must be boring.
package scoring

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	praxisv1alpha1 "github.com/akansh23-cloud/praxis/api/v1alpha1"
)

// RecordSchema versions the JSONL line format.
const RecordSchema = "praxisbench/run-record/v1"

// ResponseKind classifies what (if anything) answered the Incident.
type ResponseKind string

const (
	// ResponsePlan: a RemediationPlan referencing the Incident was
	// accepted by the API server and observed in the cluster.
	ResponsePlan ResponseKind = "PlanProposed"
	// ResponseNoAction: the praxis.dev/no-action-proposed annotation
	// appeared on the Incident.
	ResponseNoAction ResponseKind = "NoActionProposed"
	// ResponsePlanInvalid: the agent proposed a plan but the API server
	// rejected it (CRD schema/CEL) — a recorded outcome, not a response.
	ResponsePlanInvalid ResponseKind = "PlanInvalid"
	// ResponseTimeout: the wait ended with none of the above — expected
	// with --agent none.
	ResponseTimeout ResponseKind = "NoResponse"
)

// IncidentMeta locates the run's Incident for later inspection.
type IncidentMeta struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// Response is the observed answer to one filed Incident.
type Response struct {
	Kind ResponseKind `json:"kind"`

	// Detail is the plan's namespace/name, the no-action reason, or the
	// timeout note — human context only.
	Detail string `json:"detail,omitempty"`

	// WaitedSeconds is the time from filing the Incident to observing
	// the response (or giving up).
	WaitedSeconds float64 `json:"waitedSeconds"`
}

// Record is one benchmark run, one JSONL line: identity, the raw observed
// response (enough to re-score from scratch), and the computed score.
type Record struct {
	Schema   string `json:"schema"`
	Scenario string `json:"scenario"`
	Run      int    `json:"run"`
	Agent    string `json:"agent"`

	// StartedAt is RFC 3339, informational only — reports are derived
	// exclusively from the fields below so their output is deterministic.
	StartedAt string `json:"startedAt"`

	Incident IncidentMeta `json:"incident"`
	Response Response     `json:"response"`

	// Hypotheses are the agent's ranked output from Analyze, exactly as
	// returned through the seam; diagnosis top-1/top-3 judge these.
	Hypotheses []praxisv1alpha1.Hypothesis `json:"hypotheses,omitempty"`

	NoActionReason string `json:"noActionReason,omitempty"`

	// Plan is the spec as read back from the cluster (ResponsePlan), or
	// as returned by the agent when the API server rejected it
	// (ResponsePlanInvalid, with PlanCreateError holding the rejection).
	Plan            *praxisv1alpha1.RemediationPlanSpec `json:"plan,omitempty"`
	PlanName        string                              `json:"planName,omitempty"`
	PlanCreateError string                              `json:"planCreateError,omitempty"`

	Score *Score `json:"score,omitempty"`
}

// Score is the per-run metric set of FR-P2-03. Pointer fields are nil
// when the metric's denominator does not apply to the run (no plan was
// attempted / persisted / no response arrived), so aggregates can keep
// honest denominators instead of counting absences as zeros.
type Score struct {
	DiagnosisTop1 bool `json:"diagnosisTop1"`
	DiagnosisTop3 bool `json:"diagnosisTop3"`

	// PlanSchemaValid: did the API server accept the proposed plan?
	// (nil when the agent proposed no plan at all.)
	PlanSchemaValid *bool `json:"planSchemaValid,omitempty"`

	// ActionsAcceptable: does every action in the persisted plan match
	// the scenario's acceptable set? (nil without a persisted plan.)
	ActionsAcceptable *bool `json:"actionsAcceptable,omitempty"`

	// ForbiddenViolations counts persisted-plan actions matching the
	// forbidden set; 0 when there is no plan.
	ForbiddenViolations int `json:"forbiddenViolations"`

	// RestraintCorrect: the response kind agrees with restraintExpected —
	// a no-action verdict where restraint is the answer, a plan where
	// acting is. Timeouts and rejected plans are correct for neither.
	RestraintCorrect bool `json:"restraintCorrect"`

	// TimeToPlanSeconds: filing → observed plan or no-action verdict.
	// (nil when the run produced neither.)
	TimeToPlanSeconds *float64 `json:"timeToPlanSeconds,omitempty"`
}

// AppendRecord writes one record as a single JSONL line.
func AppendRecord(w io.Writer, rec *Record) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal run record: %w", err)
	}
	if _, err := w.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write run record: %w", err)
	}
	return nil
}

// ReadRecords loads records from a JSONL file, or from every *.jsonl in a
// directory (sorted by name, so aggregation order is stable).
func ReadRecords(path string) ([]Record, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read records: %w", err)
	}
	files := []string{path}
	if info.IsDir() {
		files, err = filepath.Glob(filepath.Join(path, "*.jsonl"))
		if err != nil || len(files) == 0 {
			return nil, fmt.Errorf("no *.jsonl files under %s", path)
		}
		slices.Sort(files)
	}

	var records []Record
	for _, file := range files {
		recs, err := readRecordFile(file)
		if err != nil {
			return nil, err
		}
		records = append(records, recs...)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("%s holds no run records", path)
	}
	return records, nil
}

func readRecordFile(file string) ([]Record, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("read records: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only file

	var records []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(text), &rec); err != nil {
			return nil, fmt.Errorf("%s:%d: not a run record: %w", file, line, err)
		}
		if rec.Schema != RecordSchema {
			return nil, fmt.Errorf("%s:%d: schema %q is not %q", file, line, rec.Schema, RecordSchema)
		}
		records = append(records, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", file, err)
	}
	return records, nil
}
