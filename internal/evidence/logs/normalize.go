/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

package logs

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxLineBytes bounds one normalized line before templating, so no
// template or exemplar can exceed it (plus the cut marker). The §6 item
// cap is 4 KiB; a template and an exemplar of at most 1 KiB each leave
// the item's labels and counts comfortable room.
const MaxLineBytes = 1024

// ansiCSI matches ECMA-48 control sequences (ESC [ … final byte): colour,
// cursor and erase codes that terminal-oriented loggers emit.
var ansiCSI = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// Normalize makes one raw line fit for templating: control sequences and
// control characters are removed (a tab becomes a space), invalid UTF-8 is
// replaced, surrounding whitespace is trimmed, and the line is cut at
// MaxLineBytes on a rune boundary with the cut marked "…".
//
// This is hygiene, not interpretation: nothing in a line is ever acted
// upon. Instruction-position handling of bundle text is the LLM agent's
// concern (Session 3.3); here a line is data that merely gets tidier.
func Normalize(line string) string {
	s := ansiCSI.ReplaceAllString(line, "")
	s = strings.ToValidUTF8(s, "�")

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			// control character: dropped
		default:
			b.WriteRune(r)
		}
	}
	s = strings.TrimSpace(b.String())

	if len(s) > MaxLineBytes {
		cut := MaxLineBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}
