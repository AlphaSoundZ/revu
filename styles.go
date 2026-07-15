package main

import "github.com/charmbracelet/lipgloss"

var (
	colStaged      = lipgloss.Color("#29D398") // staged, not yet reviewed
	colReviewed    = lipgloss.Color("#26BBD9") // reviewed
	colSkimmed     = lipgloss.Color("#B877DB") // skimmed (überflogen)
	colUnstaged    = lipgloss.Color("#ECEFF4") // unstaged / untracked
	colPartial     = lipgloss.Color("#FAB795") // partially reviewed
	colRemoved     = lipgloss.Color("#E95678") // staged deleted (-) lines
	colRemSoft     = lipgloss.Color("#F8CCD6") // unstaged deletions: near-white red tint
	colAddSoft     = lipgloss.Color("#A9EFD2") // unstaged additions: faint green tint
	colContext     = lipgloss.Color("#ECEFF4")
	colDim         = lipgloss.Color("#6C7A96")
	colCursorBg    = lipgloss.Color("#33394A")
	colVisualBg    = lipgloss.Color("#3D4666")
	colBorderF     = lipgloss.Color("#29D398")
	colBorderU     = lipgloss.Color("#3B4261")
	colSearchBg    = lipgloss.Color("#B877DB") // search match highlight
	colSearchCurBg = lipgloss.Color("#8A3FB8") // current search match (darker)

	stStaged    = lipgloss.NewStyle().Foreground(colStaged)
	stReviewed  = lipgloss.NewStyle().Foreground(colReviewed)
	stSkimmed   = lipgloss.NewStyle().Foreground(colSkimmed)
	stUnstaged  = lipgloss.NewStyle().Foreground(colUnstaged)
	stPartial   = lipgloss.NewStyle().Foreground(colPartial)
	stRemoved   = lipgloss.NewStyle().Foreground(colRemoved)
	stRemSoft   = lipgloss.NewStyle().Foreground(colRemSoft)
	stAddSoft   = lipgloss.NewStyle().Foreground(colAddSoft)
	stContext   = lipgloss.NewStyle().Foreground(colContext)
	stDim       = lipgloss.NewStyle().Foreground(colDim)
	stDir       = lipgloss.NewStyle().Foreground(colUnstaged).Bold(true)
	stTitle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ECEFF4")).Bold(true)
	stStatus    = lipgloss.NewStyle().Foreground(colDim)
	stStatusMsg = lipgloss.NewStyle().Foreground(colPartial)
	stSearch    = lipgloss.NewStyle().Background(colSearchBg).Foreground(lipgloss.Color("#1A1C23"))
	stSearchCur = lipgloss.NewStyle().Background(colSearchCurBg).Foreground(lipgloss.Color("#ECEFF4"))
	stBarTrack  = lipgloss.NewStyle().Foreground(colBorderU)
	stBarThumb  = lipgloss.NewStyle().Foreground(colDim)
)

// dirStyle picks the tree color for a folder from its aggregated content:
// blue fully reviewed (violet when skimmed marks are among them), orange
// partially, otherwise white.
func dirStyle(rev, tot int, skim bool) lipgloss.Style {
	if tot > 0 && rev == tot {
		if skim {
			return stSkimmed.Bold(true)
		}
		return stReviewed.Bold(true)
	}
	if rev > 0 {
		return stPartial.Bold(true)
	}
	return stDir
}

// fileStyle picks the tree color for a file: gray when nothing is
// reviewable, green staged, orange partially reviewed, blue fully
// reviewed (violet when skimmed marks are among them).
func fileStyle(e *FileEntry, rev, tot int, skim bool) lipgloss.Style {
	if e.Excluded {
		return stDim
	}
	if !e.Staged {
		return stUnstaged
	}
	if tot > 0 && rev == tot {
		if skim {
			return stSkimmed
		}
		return stReviewed
	}
	if rev > 0 {
		return stPartial
	}
	return stStaged
}
