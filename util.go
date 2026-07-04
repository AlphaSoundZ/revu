package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

func padRight(s string, w int) string {
	r := []rune(s)
	if len(r) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(r))
}

func expandTabs(s string) string {
	return strings.ReplaceAll(s, "\t", "    ")
}

// highlightMatches renders text in base style with every case-insensitive
// occurrence of query rendered in hl style.
func highlightMatches(text, query string, base, hl lipgloss.Style) string {
	if query == "" {
		return base.Render(text)
	}
	lower := strings.ToLower(text)
	lq := strings.ToLower(query)
	if len(lower) != len(text) {
		// unicode case folding changed byte offsets; skip highlighting
		return base.Render(text)
	}
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(lower[i:], lq)
		if j < 0 {
			b.WriteString(base.Render(text[i:]))
			break
		}
		j += i
		b.WriteString(base.Render(text[i:j]))
		b.WriteString(hl.Render(text[j : j+len(lq)]))
		i = j + len(lq)
	}
	return b.String()
}
