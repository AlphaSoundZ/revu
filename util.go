package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ansiBg converts a #RRGGBB color to a raw SGR background sequence, for
// re-applying a background inside chroma-styled text (whose resets would
// otherwise clear it).
func ansiBg(c lipgloss.Color) string {
	s := string(c)
	if len(s) != 7 || s[0] != '#' {
		return ""
	}
	var r, g, b int
	fmt.Sscanf(s[1:], "%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

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

// overlay places a rendered box centered over the screen, keeping the
// background visible around it. Lines are cut ANSI-aware so styled
// background text keeps its colors.
func overlay(bg, box string, w, h int) string {
	boxLines := strings.Split(box, "\n")
	bw := 0
	for _, l := range boxLines {
		if lw := ansi.StringWidth(l); lw > bw {
			bw = lw
		}
	}
	x, y := max(0, (w-bw)/2), max(0, (h-len(boxLines))/2)
	bgLines := strings.Split(bg, "\n")
	for len(bgLines) < y+len(boxLines) {
		bgLines = append(bgLines, "")
	}
	for i, bl := range boxLines {
		row := bgLines[y+i]
		left := ansi.Truncate(row, x, "")
		if pad := x - ansi.StringWidth(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		if lw := ansi.StringWidth(bl); lw < bw {
			bl += strings.Repeat(" ", bw-lw)
		}
		right := ansi.TruncateLeft(row, x+bw, "")
		bgLines[y+i] = left + "\x1b[0m" + bl + right
	}
	return strings.Join(bgLines, "\n")
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
