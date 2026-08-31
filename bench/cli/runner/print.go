/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package runner

import (
	"fmt"
	"io"
	"time"
)

// printer is the runner's one voice. Write errors to a terminal are not
// actionable mid-run, so they are consciously dropped here and nowhere else.
type printer struct {
	w io.Writer
}

func (p printer) f(format string, args ...any) {
	_, _ = fmt.Fprintf(p.w, format+"\n", args...)
}

// timing is one measured pipeline phase.
type timing struct {
	name string
	d    time.Duration
}

// fmtDur keeps sub-second phases readable and long ones tidy.
func fmtDur(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

// printSummary reports every outcome and every phase timing — the numbers
// the ≤10-minutes-to-fault-ready budget is judged against.
func (r *Runner) printSummary(outcomes []outcome) {
	r.out.f("")
	r.out.f("==> outcomes")
	for _, oc := range outcomes {
		detail := ""
		if oc.Response.Detail != "" {
			detail = " (" + oc.Response.Detail + ")"
		}
		r.out.f("    run %d: %s%s after %s", oc.Run, oc.Response.Kind, detail, fmtDur(oc.Response.Waited))
	}
	r.out.f("==> timings")
	var total time.Duration
	for _, tm := range r.timings {
		r.out.f("    %-26s %s", tm.name, fmtDur(tm.d))
		total += tm.d
	}
	r.out.f("    %-26s %s", "total", fmtDur(total))
}
