package main

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// sb is a strings.Builder that writes several parts without concatenating
// them into an intermediate string first.
type sb struct{ strings.Builder }

// hints renders "key desc" pairs: hints("a", "add", "d", "remove").
func hints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, hintKeyStyle.Render(pairs[i])+" "+hintStyle.Render(pairs[i+1]))
	}
	return strings.Join(parts, hintStyle.Render("  ·  "))
}

// truncate cuts s to w cells, ignoring ANSI styling, adding an ellipsis when cut.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// truncateLeft keeps the tail of s (useful for paths).
func truncateLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return "…" + string(r[len(r)-w+1:])
}

// wrap breaks s into lines no longer than w runes (word boundaries preferred).
func wrap(s string, w int) string {
	if w <= 0 {
		return s
	}
	var out []string
	for para := range strings.SplitSeq(s, "\n") {
		words := strings.Fields(para)
		line := ""
		for _, word := range words {
			for len([]rune(word)) > w {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, string([]rune(word)[:w]))
				word = string([]rune(word)[w:])
			}
			if line == "" {
				line = word
			} else if len([]rune(line))+1+len([]rune(word)) <= w {
				line += " " + word
			} else {
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// add writes all parts.
func (s *sb) add(parts ...string) {
	for _, p := range parts {
		s.WriteString(p)
	}
}

// line writes all parts followed by a newline.
func (s *sb) line(parts ...string) {
	s.add(parts...)
	s.WriteByte('\n')
}
