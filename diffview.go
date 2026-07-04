package main

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type rowKind int

const (
	rowHunkHeader rowKind = iota
	rowLine
	rowInfo
)

type dRow struct {
	kind rowKind
	hunk int
	line int
	text string
}

// DiffView renders one file's diff and tracks the hunk/line cursor,
// hunk-vs-line mode and visual selection.
type DiffView struct {
	store     *Store
	entry     *FileEntry
	lineMode  bool
	visual    bool
	anchor    int
	cursor    int // index into sels
	scroll    int
	rows      []dRow
	sels      []int // row indices selectable in the current mode
	h         int   // last rendered height, used for paging
	free      bool  // free-scrolled via J/K; suspends cursor-follow
	searchRow int   // row of the current search match, -1 when none
}

func (d *DiffView) SetEntry(e *FileEntry) {
	same := e != nil && d.entry != nil && e.Path == d.entry.Path
	d.entry = e
	d.visual = false
	d.searchRow = -1
	if !same {
		d.lineMode = false
		d.cursor = 0
		d.scroll = 0
		d.free = false
	}
	d.rebuild()
}

func (d *DiffView) rebuild() {
	d.rows = nil
	d.sels = nil
	if d.entry == nil {
		return
	}
	if d.entry.Binary {
		d.rows = append(d.rows, dRow{kind: rowInfo, hunk: -1, line: -1, text: "(binary file)"})
		return
	}
	for hi, h := range d.entry.Hunks {
		d.rows = append(d.rows, dRow{kind: rowHunkHeader, hunk: hi, line: -1, text: h.Header})
		if !d.lineMode {
			d.sels = append(d.sels, len(d.rows)-1)
		}
		for li, l := range h.Lines {
			d.rows = append(d.rows, dRow{kind: rowLine, hunk: hi, line: li, text: string(l.Origin) + l.Text})
			if d.lineMode && (l.Origin == '+' || l.Origin == '-') {
				d.sels = append(d.sels, len(d.rows)-1)
			}
		}
	}
	if len(d.sels) == 0 {
		d.cursor = 0
	} else {
		d.cursor = clamp(d.cursor, 0, len(d.sels)-1)
	}
}

func (d *DiffView) Move(delta int) {
	if len(d.sels) == 0 {
		return
	}
	d.free = false
	d.cursor = clamp(d.cursor+delta, 0, len(d.sels)-1)
}

func (d *DiffView) Top() {
	d.free = false
	d.cursor = 0
}

func (d *DiffView) Bottom() {
	d.free = false
	if len(d.sels) > 0 {
		d.cursor = len(d.sels) - 1
	}
}

// Scroll moves the viewport without moving the cursor; the view stops
// following the cursor until the next cursor movement.
func (d *DiffView) Scroll(delta int) {
	if len(d.rows) == 0 {
		return
	}
	h := d.h
	if h <= 0 {
		h = 20
	}
	d.scroll = clamp(d.scroll+delta, 0, max(0, len(d.rows)-h))
	d.free = true
}

// ToggleMode switches between hunk and line granularity, keeping the
// cursor near its previous position.
func (d *DiffView) ToggleMode() {
	curRow := 0
	if len(d.sels) > 0 {
		curRow = d.sels[d.cursor]
	}
	curHunk := 0
	if curRow < len(d.rows) {
		curHunk = d.rows[curRow].hunk
	}
	d.lineMode = !d.lineMode
	d.visual = false
	d.rebuild()
	if len(d.sels) == 0 {
		d.cursor = 0
		return
	}
	if d.lineMode {
		d.cursor = len(d.sels) - 1
		for i, r := range d.sels {
			if r >= curRow {
				d.cursor = i
				break
			}
		}
	} else {
		d.cursor = clamp(curHunk, 0, len(d.sels)-1)
	}
}

func (d *DiffView) StartVisual() {
	if len(d.sels) == 0 {
		return
	}
	if !d.lineMode {
		d.ToggleMode()
	}
	if len(d.sels) == 0 {
		return
	}
	d.visual = true
	d.anchor = d.cursor
}

// ToggleReviewed flips review state for the current hunk, line or visual
// range. Returns a status message when nothing could be toggled.
func (d *DiffView) ToggleReviewed() string {
	if d.entry == nil {
		return ""
	}
	if d.entry.Excluded {
		return "file is excluded from review (.revu/config.json)"
	}
	if d.entry.Binary {
		e := d.entry
		if !e.Staged || e.BinaryID == "" {
			return "only staged files can be marked as reviewed"
		}
		d.store.Set(e.BinaryID, !d.store.Has(e.BinaryID))
		if err := d.store.Save(); err != nil {
			return "failed to save review state: " + err.Error()
		}
		return ""
	}
	if len(d.sels) == 0 {
		return ""
	}
	if !d.lineMode {
		h := d.entry.Hunks[d.rows[d.sels[d.cursor]].hunk]
		if !h.Reviewable {
			return "only staged hunks can be marked as reviewed"
		}
		all := true
		for _, l := range h.Lines {
			if (l.Origin == '+' || l.Origin == '-') && !d.store.Has(l.ID) {
				all = false
				break
			}
		}
		for _, l := range h.Lines {
			if l.Origin == '+' || l.Origin == '-' {
				d.store.Set(l.ID, !all)
			}
		}
	} else {
		lo, hi := d.cursor, d.cursor
		if d.visual {
			if d.anchor < lo {
				lo = d.anchor
			}
			if d.anchor > hi {
				hi = d.anchor
			}
		}
		var ids []string
		all := true
		for i := lo; i <= hi; i++ {
			r := d.rows[d.sels[i]]
			h := d.entry.Hunks[r.hunk]
			if !h.Reviewable {
				continue
			}
			l := h.Lines[r.line]
			ids = append(ids, l.ID)
			if !d.store.Has(l.ID) {
				all = false
			}
		}
		d.visual = false
		if len(ids) == 0 {
			return "only staged lines can be marked as reviewed"
		}
		for _, id := range ids {
			d.store.Set(id, !all)
		}
	}
	if err := d.store.Save(); err != nil {
		return "failed to save review state: " + err.Error()
	}
	return ""
}

// SearchJump moves to the next row (cyclic) containing the query. When the
// match is selectable in the current mode the cursor moves there, otherwise
// the viewport free-scrolls to show it.
func (d *DiffView) SearchJump(q string, dir int) bool {
	if d.entry == nil || len(d.rows) == 0 || q == "" {
		return false
	}
	lq := strings.ToLower(q)
	cur := d.searchRow
	if cur < 0 {
		if len(d.sels) > 0 {
			cur = d.sels[d.cursor]
			if dir > 0 {
				cur-- // include the cursor row on the first jump
			}
		} else {
			cur = d.scroll - 1
		}
	}
	n := len(d.rows)
	target := -1
	for step := 1; step <= n; step++ {
		i := ((cur+dir*step)%n + n) % n
		if strings.Contains(strings.ToLower(d.rows[i].text), lq) {
			target = i
			break
		}
	}
	if target < 0 {
		return false
	}
	d.searchRow = target
	for i, s := range d.sels {
		if s == target {
			d.cursor = i
			d.free = false
			return true
		}
	}
	h := d.h
	if h <= 0 {
		h = 20
	}
	d.scroll = clamp(target-h/2, 0, max(0, len(d.rows)-h))
	d.free = true
	return true
}

// CurrentFilePath returns the file the cursor is on; hunks carry their own
// path in the commit view, otherwise it is the entry's path.
func (d *DiffView) CurrentFilePath() string {
	if d.entry == nil {
		return ""
	}
	if len(d.sels) > 0 {
		r := d.rows[d.sels[d.cursor]]
		if r.kind != rowInfo && r.hunk >= 0 && r.hunk < len(d.entry.Hunks) {
			if fp := d.entry.Hunks[r.hunk].FilePath; fp != "" {
				return fp
			}
		}
	}
	return d.entry.Path
}

// lineNumIn maps a hunk line index to a line number in the new file.
func (d *DiffView) lineNumIn(hunkIdx, lineIdx int) int {
	h := d.entry.Hunks[hunkIdx]
	start := parseNewStart(h.Header)
	if start <= 0 {
		if d.entry.Untracked {
			start = 1
		} else {
			return 0
		}
	}
	n := start
	for i := 0; i < lineIdx && i < len(h.Lines); i++ {
		if h.Lines[i].Origin != '-' {
			n++
		}
	}
	return n
}

// CurrentLineInFile maps the cursor to a line number in the new file,
// used for opening the editor at the right position.
func (d *DiffView) CurrentLineInFile() int {
	if d.entry == nil || len(d.sels) == 0 {
		return 0
	}
	r := d.rows[d.sels[d.cursor]]
	if r.kind == rowInfo || r.hunk < 0 {
		return 0
	}
	if r.kind == rowHunkHeader {
		return d.lineNumIn(r.hunk, 0)
	}
	return d.lineNumIn(r.hunk, r.line)
}

// SelectionRange returns the new-file line range of the current selection:
// the changed lines of the hunk in hunk mode, the current line or visual
// range in line mode. Returns 0,0 when nothing is selected.
func (d *DiffView) SelectionRange() (int, int) {
	if d.entry == nil || len(d.sels) == 0 {
		return 0, 0
	}
	if !d.lineMode {
		r := d.rows[d.sels[d.cursor]]
		if r.kind == rowInfo || r.hunk < 0 {
			return 0, 0
		}
		changed := d.entry.Hunks[r.hunk].ChangedIdx()
		if len(changed) == 0 {
			return 0, 0
		}
		return d.lineNumIn(r.hunk, changed[0]), d.lineNumIn(r.hunk, changed[len(changed)-1])
	}
	lo, hi := d.cursor, d.cursor
	if d.visual {
		if d.anchor < lo {
			lo = d.anchor
		}
		if d.anchor > hi {
			hi = d.anchor
		}
	}
	rl, rh := d.rows[d.sels[lo]], d.rows[d.sels[hi]]
	return d.lineNumIn(rl.hunk, rl.line), d.lineNumIn(rh.hunk, rh.line)
}

func parseOldStart(header string) int {
	for _, tok := range strings.Fields(header) {
		if strings.HasPrefix(tok, "-") {
			num := strings.TrimPrefix(tok, "-")
			if i := strings.IndexByte(num, ','); i >= 0 {
				num = num[:i]
			}
			if n, err := strconv.Atoi(num); err == nil {
				return n
			}
		}
	}
	return 0
}

func parseNewStart(header string) int {
	for _, tok := range strings.Fields(header) {
		if strings.HasPrefix(tok, "+") {
			num := strings.TrimPrefix(tok, "+")
			if i := strings.IndexByte(num, ','); i >= 0 {
				num = num[:i]
			}
			if n, err := strconv.Atoi(num); err == nil {
				return n
			}
		}
	}
	return 0
}

func (d *DiffView) ensureVisible(h int) {
	maxScroll := max(0, len(d.rows)-h)
	if d.free {
		d.scroll = clamp(d.scroll, 0, maxScroll)
		return
	}
	if len(d.sels) == 0 {
		d.scroll = clamp(d.scroll, 0, maxScroll)
		return
	}
	row := d.sels[d.cursor]
	if !d.lineMode {
		endRow := row
		for endRow+1 < len(d.rows) && d.rows[endRow+1].kind == rowLine && d.rows[endRow+1].hunk == d.rows[row].hunk {
			endRow++
		}
		if endRow-row+1 >= h {
			d.scroll = row
		} else {
			if row < d.scroll {
				d.scroll = row
			}
			if endRow >= d.scroll+h {
				d.scroll = endRow - h + 1
			}
		}
	} else {
		if row < d.scroll {
			d.scroll = row
		}
		if row >= d.scroll+h {
			d.scroll = row - h + 1
		}
	}
	d.scroll = clamp(d.scroll, 0, maxScroll)
}

func (d *DiffView) rowStyle(r dRow) lipgloss.Style {
	switch r.kind {
	case rowInfo:
		if d.entry.Binary && d.store.Has(d.entry.BinaryID) {
			return stReviewed
		}
		return stDim
	case rowHunkHeader:
		h := d.entry.Hunks[r.hunk]
		if !h.Reviewable || d.entry.Excluded {
			return stDim.Bold(true)
		}
		rev, tot := hunkCounts(h, d.store)
		switch {
		case tot > 0 && rev == tot:
			return stReviewed.Bold(true)
		case rev > 0:
			return stPartial.Bold(true)
		default:
			return stStaged.Bold(true)
		}
	default:
		h := d.entry.Hunks[r.hunk]
		l := h.Lines[r.line]
		if l.Origin == ' ' {
			return stContext
		}
		reviewable := h.Reviewable && !d.entry.Excluded
		if reviewable && d.store.Has(l.ID) {
			return stReviewed
		}
		if !reviewable {
			if l.Origin == '-' {
				return stRemSoft
			}
			return stAddSoft
		}
		if l.Origin == '-' {
			return stRemoved
		}
		return stStaged
	}
}

func (d *DiffView) View(w, h int, focused bool, query string) string {
	d.h = h
	if d.entry == nil {
		return stDim.Render("(no file selected)")
	}
	if len(d.rows) == 0 {
		return stDim.Render("(no changes)")
	}
	d.ensureVisible(h)
	n := len(d.rows)
	showBar := n > h
	tw := w
	barTop, thumbH := 0, 0
	if showBar {
		tw = w - 1
		thumbH = max(1, h*h/n)
		if maxScroll := n - h; maxScroll > 0 {
			barTop = d.scroll * (h - thumbH) / maxScroll
		}
	}
	curRow, curHunk := -1, -1
	if len(d.sels) > 0 {
		curRow = d.sels[d.cursor]
		curHunk = d.rows[curRow].hunk
	}
	visLo, visHi := -1, -1
	if d.visual && len(d.sels) > 0 {
		lo, hi := d.cursor, d.anchor
		if lo > hi {
			lo, hi = hi, lo
		}
		visLo, visHi = d.sels[lo], d.sels[hi]
	}
	var b strings.Builder
	end := min(len(d.rows), d.scroll+h)
	for i := d.scroll; i < end; i++ {
		r := d.rows[i]
		st := d.rowStyle(r)
		text := truncate(expandTabs(r.text), tw)
		highlight := false
		if focused {
			switch {
			case d.visual && i >= visLo && i <= visHi:
				st = st.Background(colVisualBg)
				highlight = true
			case d.lineMode && i == curRow:
				st = st.Background(colCursorBg)
				highlight = true
			case !d.lineMode && curHunk >= 0 && r.kind != rowInfo && r.hunk == curHunk:
				st = st.Background(colCursorBg)
				highlight = true
			}
		}
		if highlight || showBar {
			text = padRight(text, tw)
		}
		hl := stSearch
		if i == d.searchRow {
			hl = stSearchCur // the current n/N match
		}
		b.WriteString(highlightMatches(text, query, st, hl))
		if showBar {
			slot := i - d.scroll
			if slot >= barTop && slot < barTop+thumbH {
				b.WriteString(stBarThumb.Render("█"))
			} else {
				b.WriteString(stBarTrack.Render("│"))
			}
		}
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
