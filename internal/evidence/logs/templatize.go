/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package logs

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

// Templatize clusters log lines into templates the way Drain does — a
// fixed-depth parse tree over (token count, first token) whose leaves hold
// clusters, a token-equality similarity threshold, and wildcard merging —
// with the two adaptations ADR-007 records to make the result a pure
// function of the multiset of lines:
//
//  1. Lines are sorted before insertion. Drain's clustering depends on
//     arrival order (which cluster exists first decides what later lines
//     join); a canonical order removes that dependence entirely, so the
//     same set of lines in any order yields byte-identical clusters.
//  2. Obvious variable values (timestamps, IPs, UUIDs, hex ids, numbers)
//     are masked to the wildcard before tokenizing, so they never keep two
//     lines of the same shape apart.
//
// There is no randomness, no clock, no map iteration anywhere on the
// output path. Thresholds are fixed constants.

// Wildcard marks a variable position in a template.
const Wildcard = "<*>"

const (
	// similarityThreshold is the fraction of equal token positions a line
	// must share with a cluster's template to join it (Drain's st).
	similarityThreshold = 0.5

	// maxChildren bounds the parse tree's width under a token count
	// (Drain's default); once reached, unseen first tokens route to the
	// wildcard child instead of opening a new one.
	maxChildren = 100
)

// Cluster is one log template: the template text, how many lines it
// covers, and exactly one exemplar — the bytewise-smallest member line.
type Cluster struct {
	Template string
	Count    int
	Exemplar string
}

// variablePatterns are masked to the wildcard before tokenizing, in this
// order (earlier patterns claim their text first, so a UUID is one
// wildcard rather than several hex runs).
var variablePatterns = []*regexp.Regexp{
	// klog headers: severity letter plus MMDD (I0912) — a date, so a
	// template must not change with the calendar.
	regexp.MustCompile(`\b[IWEF]\d{4}\b`),
	// RFC 3339 / ISO 8601 timestamps, with or without fraction and zone.
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`),
	// Clock times and bare dates.
	regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}(?:\.\d+)?\b`),
	regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`),
	// UUIDs.
	regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`),
	// IPv4 addresses with an optional port.
	regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d{1,5})?\b`),
}

// hexPattern catches hexadecimal identifiers of eight or more characters;
// a run must also contain a digit to count (see maskVariables), so plain
// words spelled from a–f alone are left as they are.
var hexPattern = regexp.MustCompile(`\b[0-9a-f]{8,}\b|\b[0-9A-F]{8,}\b`)

// numberPattern catches standalone numbers, optionally decimal and with a
// short unit suffix (37ms, 1.5Gi, 95%). Digits inside identifiers (v1,
// http2, x2m8q) have no word boundary before them and are left alone —
// the merge step decides whether those positions vary.
var numberPattern = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:[a-zA-Zµ%]{1,3})?\b`)

func maskVariables(line string) string {
	s := line
	for _, p := range variablePatterns {
		s = p.ReplaceAllString(s, Wildcard)
	}
	s = hexPattern.ReplaceAllStringFunc(s, func(m string) string {
		if hasDigit(m) {
			return Wildcard
		}
		return m
	})
	return numberPattern.ReplaceAllString(s, Wildcard)
}

func hasDigit(s string) bool {
	return strings.ContainsAny(s, "0123456789")
}

// routeKey is the parse-tree key of a first token: Drain routes any token
// carrying a digit (or already wildcarded) through the wildcard branch,
// since such tokens are usually variables.
func routeKey(token string) string {
	if strings.Contains(token, Wildcard) || hasDigit(token) {
		return Wildcard
	}
	return token
}

type cluster struct {
	tokens   []string
	count    int
	exemplar string
}

// leaf holds the clusters under one (token count, first token) path.
type leaf struct {
	clusters []*cluster
}

// lengthNode is the tree level under a token count: one child per first
// token, plus the wildcard child.
type lengthNode struct {
	children map[string]*leaf
	owned    int // non-wildcard children created so far
}

// Templatize clusters lines into templates. The result is sorted by count
// (most frequent first), then template, then exemplar, and is identical
// for any ordering of the same lines. Blank lines contribute nothing.
func Templatize(lines []string) []Cluster {
	work := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			work = append(work, l)
		}
	}
	slices.Sort(work)

	tree := map[int]*lengthNode{}
	var all []*cluster
	for _, line := range work {
		tokens := strings.Fields(maskVariables(line))
		if len(tokens) == 0 {
			continue
		}
		lf := route(tree, tokens)

		var best *cluster
		bestSim := -1.0
		for _, c := range lf.clusters {
			if sim := similarity(c.tokens, tokens); sim > bestSim {
				best, bestSim = c, sim
			}
		}
		if best != nil && bestSim >= similarityThreshold {
			for i := range tokens {
				if best.tokens[i] != tokens[i] {
					best.tokens[i] = Wildcard
				}
			}
			best.count++
			if line < best.exemplar {
				best.exemplar = line
			}
			continue
		}
		c := &cluster{tokens: slices.Clone(tokens), count: 1, exemplar: line}
		lf.clusters = append(lf.clusters, c)
		all = append(all, c)
	}

	out := make([]Cluster, 0, len(all))
	for _, c := range all {
		out = append(out, Cluster{
			Template: strings.Join(c.tokens, " "),
			Count:    c.count,
			Exemplar: c.exemplar,
		})
	}
	slices.SortFunc(out, func(a, b Cluster) int {
		return cmp.Or(
			cmp.Compare(b.Count, a.Count),
			cmp.Compare(a.Template, b.Template),
			cmp.Compare(a.Exemplar, b.Exemplar),
		)
	})
	return out
}

// route walks (token count, first token) to the leaf a line belongs to,
// creating nodes as needed under the width bound.
func route(tree map[int]*lengthNode, tokens []string) *leaf {
	ln := tree[len(tokens)]
	if ln == nil {
		ln = &lengthNode{children: map[string]*leaf{}}
		tree[len(tokens)] = ln
	}
	key := routeKey(tokens[0])
	if key != Wildcard {
		if _, ok := ln.children[key]; !ok {
			if ln.owned >= maxChildren {
				key = Wildcard
			} else {
				ln.owned++
			}
		}
	}
	lf := ln.children[key]
	if lf == nil {
		lf = &leaf{}
		ln.children[key] = lf
	}
	return lf
}

// similarity is the fraction of positions where the template and the line
// carry the same token — a wildcard in the template against a concrete
// token does not count, two wildcards do. Templates and lines under one
// leaf always have the same length.
func similarity(template, tokens []string) float64 {
	equal := 0
	for i := range tokens {
		if template[i] == tokens[i] {
			equal++
		}
	}
	return float64(equal) / float64(len(tokens))
}
