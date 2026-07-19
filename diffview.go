package main

import (
	"fmt"
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
	oldN int // old-file line number, 0 = not on that side
	newN int // new-file line number, 0 = not on that side
	// word-diff of a lone -/+ pair (smart mode): the changed part of the
	// expanded content as rune offsets, marker excluded. An empty span
	// (pure insertion/deletion on the other side) renders as context.
	wd         bool
	wdLo, wdHi int
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
	syntax    bool  // syntax-highlight code lines (H toggles)
	smart     bool  // ctrl+w: hide ws-only hunks, word-level diffs
	numW      int   // width of one line-number column
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
	maxN := 1
	hidden := 0
	for hi, h := range d.entry.Hunks {
		if d.smart && wsOnlyHunk(h) {
			hidden++
			continue
		}
		d.rows = append(d.rows, dRow{kind: rowHunkHeader, hunk: hi, line: -1, text: h.Header})
		if !d.lineMode {
			d.sels = append(d.sels, len(d.rows)-1)
		}
		wdRem, wdAdd := -1, -1 // line indices of a lone -/+ pair
		var remSpan, addSpan [2]int
		if d.smart {
			wdRem, wdAdd = singleChange(h)
			if wdRem >= 0 {
				oldT := []rune(expandTabs(h.Lines[wdRem].Text))
				newT := []rune(expandTabs(h.Lines[wdAdd].Text))
				p, s := commonAffixes(oldT, newT)
				remSpan = [2]int{p, len(oldT) - s}
				addSpan = [2]int{p, len(newT) - s}
			}
		}
		// the deleted side is implied by the word diff, so hide it — but
		// not for pure deletions (nothing green on the + side would make
		// the change invisible) and not in line mode, where individual
		// lines must stay selectable for marking and staging
		hideRem := wdRem >= 0 && !d.lineMode && addSpan[1] > addSpan[0]
		oldN, newN := parseOldStart(h.Header), parseNewStart(h.Header)
		if oldN == 0 && newN == 0 {
			oldN, newN = 1, 1 // untracked / FILES preview: synthetic headers
		}
		for li, l := range h.Lines {
			r := dRow{kind: rowLine, hunk: hi, line: li, text: string(l.Origin) + l.Text}
			switch l.Origin {
			case '+':
				r.newN = newN
				newN++
			case '-':
				r.oldN = oldN
				oldN++
			default:
				r.oldN, r.newN = oldN, newN
				oldN++
				newN++
			}
			if r.oldN > maxN {
				maxN = r.oldN
			}
			if r.newN > maxN {
				maxN = r.newN
			}
			switch li {
			case wdRem:
				r.wd, r.wdLo, r.wdHi = true, remSpan[0], remSpan[1]
			case wdAdd:
				r.wd, r.wdLo, r.wdHi = true, addSpan[0], addSpan[1]
			}
			if li == wdRem && hideRem {
				continue // counters above stay correct
			}
			d.rows = append(d.rows, r)
			if d.lineMode && (l.Origin == '+' || l.Origin == '-') {
				d.sels = append(d.sels, len(d.rows)-1)
			}
		}
	}
	if hidden > 0 {
		d.rows = append(d.rows, dRow{kind: rowInfo, hunk: -1, line: -1,
			text: fmt.Sprintf("(%d whitespace-only hunk(s) hidden · ctrl+w shows them)", hidden)})
	}
	d.numW = max(3, len(strconv.Itoa(maxN)))
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

// Align scrolls the view so the current selection (hunk, line or visual
// range) sits at the top (zt, pos -1), middle (zz, 0) or bottom (zb, 1).
// Scrolling may run past the end — missing rows render as blank lines —
// so aligning to the top works everywhere in the file.
func (d *DiffView) Align(pos int) {
	if len(d.sels) == 0 {
		return
	}
	h := d.h
	if h <= 0 {
		h = 20
	}
	lo := d.sels[d.cursor]
	hi := lo
	if d.visual {
		l, r := d.cursor, d.anchor
		if l > r {
			l, r = r, l
		}
		lo, hi = d.sels[l], d.sels[r]
	} else if !d.lineMode {
		for hi+1 < len(d.rows) && d.rows[hi+1].kind == rowLine && d.rows[hi+1].hunk == d.rows[lo].hunk {
			hi++
		}
	}
	var target int
	switch pos {
	case -1:
		target = lo
	case 1:
		target = hi - h + 1
	default:
		target = (lo+hi)/2 - h/2
	}
	d.scroll = clamp(target, 0, max(0, len(d.rows)-1))
	d.free = true // keep the position until the cursor moves
}

// Scroll moves the viewport without moving the cursor; the view stops
// following the cursor until the next cursor movement. Like in vim the
// view can scroll until the last row sits at the top.
func (d *DiffView) Scroll(delta int) {
	if len(d.rows) == 0 {
		return
	}
	d.scroll = clamp(d.scroll+delta, 0, max(0, len(d.rows)-1))
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
// range; skim toggles the skimmed mark instead. Returns a status message
// when nothing could be toggled.
func (d *DiffView) ToggleReviewed(skim bool) string {
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
		d.store.Mark(e.BinaryID, skim, !d.store.In(e.BinaryID, skim))
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
			if (l.Origin == '+' || l.Origin == '-') && !d.store.In(l.ID, skim) {
				all = false
				break
			}
		}
		for _, l := range h.Lines {
			if l.Origin == '+' || l.Origin == '-' {
				d.store.Mark(l.ID, skim, !all)
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
			if !d.store.In(l.ID, skim) {
				all = false
			}
		}
		d.visual = false
		if len(ids) == 0 {
			return "only staged lines can be marked as reviewed"
		}
		for _, id := range ids {
			d.store.Mark(id, skim, !all)
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
		if d.entry.Untracked || d.entry.FileID != "" {
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
		// free-scrolled positions (J/K, zt/zz/zb) may overshoot the end
		d.scroll = clamp(d.scroll, 0, max(0, len(d.rows)-1))
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

// wsOnlyHunk reports whether the hunk's changes only reshuffle
// whitespace (re-indents, re-wraps): the changed lines are identical
// once every whitespace character is removed.
func wsOnlyHunk(h *Hunk) bool {
	strip := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if r != ' ' && r != '\t' && r != '\r' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	var oldB, newB strings.Builder
	changed := false
	for _, l := range h.Lines {
		switch l.Origin {
		case '-':
			changed = true
			oldB.WriteString(strip(l.Text))
		case '+':
			changed = true
			newB.WriteString(strip(l.Text))
		}
	}
	return changed && oldB.String() == newB.String()
}

// singleChange returns the line indices of the hunk's lone -/+ pair, or
// -1,-1 when the hunk changes more than one line on either side.
func singleChange(h *Hunk) (int, int) {
	rem, add := -1, -1
	for i, l := range h.Lines {
		switch l.Origin {
		case '-':
			if rem >= 0 {
				return -1, -1
			}
			rem = i
		case '+':
			if add >= 0 {
				return -1, -1
			}
			add = i
		}
	}
	if rem < 0 || add < 0 {
		return -1, -1
	}
	return rem, add
}

// commonAffixes returns the length of the common prefix and suffix of
// two rune slices; the suffix never overlaps the prefix.
func commonAffixes(a, b []rune) (int, int) {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	return p, s
}

// gutter renders the line-number column: the new-file number, falling
// back to the old-file number for deleted lines (blank for header/info
// rows).
func (d *DiffView) gutter(r dRow) string {
	n := r.newN
	if n == 0 {
		n = r.oldN
	}
	s := ""
	if n > 0 {
		s = strconv.Itoa(n)
	}
	return fmt.Sprintf("%*s ", d.numW, s)
}

// hunkPath returns the file a row belongs to, for picking a lexer;
// hunks carry their own path in the commit view.
func (d *DiffView) hunkPath(r dRow) string {
	if r.hunk >= 0 && r.hunk < len(d.entry.Hunks) {
		if fp := d.entry.Hunks[r.hunk].FilePath; fp != "" {
			return fp
		}
	}
	return d.entry.Path
}

func (d *DiffView) rowStyle(r dRow) lipgloss.Style {
	switch r.kind {
	case rowInfo:
		id := d.entry.BinaryID
		if d.entry.FileID != "" {
			id = d.entry.FileID
		}
		if d.entry.Binary && d.store.Has(id) {
			if d.store.Skimmed(id) {
				return stSkimmed
			}
			return stReviewed
		}
		if d.entry.Binary {
			if skim, ok := d.store.Permanent(d.entry.Path); ok {
				if skim {
					return stSkimmed
				}
				return stReviewed
			}
		}
		return stDim
	case rowHunkHeader:
		h := d.entry.Hunks[r.hunk]
		if !h.Reviewable || d.entry.Excluded {
			return stDim.Bold(true)
		}
		rev, tot := hunkCounts(h, d.store)
		// fully explicit marks win; the permanent mark is the default
		if tot > 0 && rev == tot {
			if hunkAnySkimmed(h, d.store) {
				return stSkimmed.Bold(true)
			}
			return stReviewed.Bold(true)
		}
		if skim, ok := d.store.Permanent(d.hunkPath(r)); ok {
			if skim {
				return stSkimmed.Bold(true)
			}
			return stReviewed.Bold(true)
		}
		switch {
		case rev > 0:
			return stPartial.Bold(true)
		default:
			// gray, not green: a green header is hard to tell apart
			// from the green added lines right below it
			return stDim.Bold(true)
		}
	default:
		h := d.entry.Hunks[r.hunk]
		l := h.Lines[r.line]
		if l.Origin == ' ' {
			return stContext
		}
		reviewable := h.Reviewable && !d.entry.Excluded
		// an explicit line mark wins; the permanent mark is the default
		if reviewable && d.store.Has(l.ID) {
			if d.store.Skimmed(l.ID) {
				return stSkimmed
			}
			return stReviewed
		}
		if reviewable {
			if skim, ok := d.store.Permanent(d.hunkPath(r)); ok {
				if skim {
					return stSkimmed
				}
				return stReviewed
			}
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
			barTop = min(d.scroll, maxScroll) * (h - thumbH) / maxScroll
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
	gutW := d.numW + 1
	cw := tw - gutW
	if cw < 8 {
		gutW, cw = 0, tw // pane too narrow for a gutter
	}
	var b strings.Builder
	lines := 0
	// writeLine finishes one screen line: pads (with the row style when
	// highlighted so the background extends) and draws the scrollbar.
	writeLine := func(content string, vis int, st lipgloss.Style, highlight bool) {
		if lines > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(content)
		if pad := tw - vis; pad > 0 {
			switch {
			case highlight:
				b.WriteString(st.Render(strings.Repeat(" ", pad)))
			case showBar:
				b.WriteString(strings.Repeat(" ", pad))
			}
		}
		if showBar {
			if lines >= barTop && lines < barTop+thumbH {
				b.WriteString(stBarThumb.Render("█"))
			} else {
				b.WriteString(stBarTrack.Render("│"))
			}
		}
		lines++
	}
	lq := strings.ToLower(query)
	// rows longer than the pane wrap onto continuation lines (blank
	// gutter, blank marker column) instead of being truncated
	for i := d.scroll; i < n && lines < h; i++ {
		r := d.rows[i]
		st := d.rowStyle(r)
		highlight := false
		bg := ""
		if focused {
			switch {
			case d.visual && i >= visLo && i <= visHi:
				st = st.Background(colVisualBg)
				highlight = true
				bg = seqVisualBg
			case d.lineMode && i == curRow:
				st = st.Background(colCursorBg)
				highlight = true
				bg = seqCursorBg
			case !d.lineMode && curHunk >= 0 && r.kind != rowInfo && r.hunk == curHunk:
				st = st.Background(colCursorBg)
				highlight = true
				bg = seqCursorBg
			}
		}
		hl := stSearch
		if i == d.searchRow {
			hl = stSearchCur // the current n/N match
		}
		full := []rune(expandTabs(r.text))
		matched := query != "" && strings.Contains(strings.ToLower(string(full)), lq)
		// smart mode paints only the changed part of a lone -/+ pair
		// red/green, the rest like context. With syntax on the whole
		// file is colored; with syntax off only the selected rows are;
		// clean mode suppresses the whole-file coloring so the word-diff
		// colors stand out. Search matches stay plain so their highlight
		// is visible.
		useWd := d.smart && r.wd && !highlight && !matched && r.kind == rowLine
		useSyn := !useWd && r.kind == rowLine && !matched && ((d.syntax && !d.smart) || highlight)
		gut := ""
		if gutW > 0 {
			gut = d.gutter(r)
		}
		for off, first := 0, true; lines < h; first = false {
			width := cw
			if !first {
				width = cw - 1 // continuation lines skip the marker column
			}
			if width < 1 {
				width = 1
			}
			seg := full[off:min(off+width, len(full))]
			var line strings.Builder
			vis := 0
			if gutW > 0 {
				if first {
					line.WriteString(st.Render(gut))
				} else {
					line.WriteString(st.Render(strings.Repeat(" ", gutW)))
				}
				vis += gutW
			}
			if !first {
				line.WriteString(st.Render(" "))
				vis++
			}
			code := seg
			if first && r.kind == rowLine && len(seg) > 0 {
				code = seg[1:] // the origin marker keeps the row style
			}
			switch {
			case useWd:
				codeOff := off - 1
				if first {
					line.WriteString(st.Render(string(seg[:1])))
					vis++
					codeOff = 0
				}
				lo := clamp(r.wdLo-codeOff, 0, len(code))
				hi := clamp(r.wdHi-codeOff, lo, len(code))
				segSt := stStaged
				if r.text[0] == '-' {
					segSt = stRemoved
				}
				line.WriteString(stContext.Render(string(code[:lo])) +
					segSt.Render(string(code[lo:hi])) +
					stContext.Render(string(code[hi:])))
				vis += len(code)
			case useSyn:
				if first {
					line.WriteString(st.Render(string(seg[:1])))
					vis++
				}
				if colored, ok := highlightLine(d.hunkPath(r), string(code)); ok {
					if bg != "" {
						colored = bg + strings.ReplaceAll(colored, "\x1b[0m", "\x1b[0m"+bg) + "\x1b[0m"
					}
					line.WriteString(colored)
				} else {
					line.WriteString(highlightMatches(string(code), query, st, hl))
				}
				vis += len(code)
			default:
				line.WriteString(highlightMatches(string(seg), query, st, hl))
				vis += len(seg)
			}
			writeLine(line.String(), vis, st, highlight)
			off += len(seg)
			if off >= len(full) {
				break
			}
		}
	}
	return b.String()
}
