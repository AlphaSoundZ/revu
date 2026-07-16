package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Editor is a small modal text editor (a vim subset) driving the inline
// review popup: hjkl/arrows, w b e, 0 ^ $, gg G, i a A I o O, x dd p, u.
type Editor struct {
	lines   [][]rune
	row     int
	col     int
	scroll  int
	insert  bool
	pending string // chord prefix: "g", "d", "c" or "ci"
	reg     []rune // last dd'd line
	hasReg  bool
	undo    []edState
}

type edState struct {
	text     string
	row, col int
}

// NewEditor starts on the given row (clamped), optionally in insert mode.
func NewEditor(text string, row int, insert bool) *Editor {
	e := &Editor{insert: insert}
	e.setText(text)
	e.row = clamp(row, 0, len(e.lines)-1)
	return e
}

func (e *Editor) setText(s string) {
	e.lines = nil
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		e.lines = append(e.lines, []rune(l))
	}
}

func (e *Editor) Text() string {
	parts := make([]string, len(e.lines))
	for i, l := range e.lines {
		parts[i] = string(l)
	}
	return strings.Join(parts, "\n")
}

func (e *Editor) line() []rune { return e.lines[e.row] }

// maxCol is the rightmost cursor column: on the last rune in normal
// mode, one past it in insert mode.
func (e *Editor) maxCol() int {
	n := len(e.line())
	if e.insert {
		return n
	}
	return max(0, n-1)
}

func (e *Editor) clampCol() { e.col = clamp(e.col, 0, e.maxCol()) }

func (e *Editor) firstNonSpace() int {
	for i, r := range e.line() {
		if r != ' ' && r != '\t' {
			return i
		}
	}
	return 0
}

func (e *Editor) pushUndo() {
	e.undo = append(e.undo, edState{e.Text(), e.row, e.col})
	if len(e.undo) > 200 {
		e.undo = e.undo[1:]
	}
}

func (e *Editor) popUndo() {
	if len(e.undo) == 0 {
		return
	}
	s := e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.setText(s.text)
	e.row = clamp(s.row, 0, len(e.lines)-1)
	e.col = s.col
	e.clampCol()
}

// HandleKey processes one key; it returns true when the editor closes
// (esc in normal mode).
func (e *Editor) HandleKey(msg tea.KeyMsg) bool {
	if e.insert {
		e.insertKey(msg)
		return false
	}
	return e.normalKey(msg)
}

func (e *Editor) insertRunes(rs []rune) {
	l := e.line()
	nl := make([]rune, 0, len(l)+len(rs))
	nl = append(nl, l[:e.col]...)
	nl = append(nl, rs...)
	nl = append(nl, l[e.col:]...)
	e.lines[e.row] = nl
	e.col += len(rs)
}

func (e *Editor) insertLineAt(row int, l []rune) {
	rest := append([][]rune{l}, e.lines[row:]...)
	e.lines = append(e.lines[:row], rest...)
}

func (e *Editor) insertKey(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyEsc:
		e.insert = false
		if e.col > 0 {
			e.col--
		}
		e.clampCol()
	case tea.KeyEnter:
		l := e.line()
		tail := append([]rune{}, l[e.col:]...)
		e.lines[e.row] = l[:e.col]
		e.insertLineAt(e.row+1, tail)
		e.row++
		e.col = 0
	case tea.KeyBackspace:
		if e.col > 0 {
			l := e.line()
			e.lines[e.row] = append(l[:e.col-1], l[e.col:]...)
			e.col--
		} else if e.row > 0 {
			prev := e.lines[e.row-1]
			e.col = len(prev)
			e.lines[e.row-1] = append(prev, e.line()...)
			e.lines = append(e.lines[:e.row], e.lines[e.row+1:]...)
			e.row--
		}
	case tea.KeySpace:
		e.insertRunes([]rune{' '})
	case tea.KeyTab:
		e.insertRunes([]rune("    "))
	case tea.KeyUp:
		if e.row > 0 {
			e.row--
			e.clampCol()
		}
	case tea.KeyDown:
		if e.row < len(e.lines)-1 {
			e.row++
			e.clampCol()
		}
	case tea.KeyLeft:
		if e.col > 0 {
			e.col--
		}
	case tea.KeyRight:
		if e.col < e.maxCol() {
			e.col++
		}
	case tea.KeyRunes:
		e.insertRunes(msg.Runes)
	}
}

func (e *Editor) normalKey(msg tea.KeyMsg) bool {
	key := msg.String()
	if e.pending != "" {
		p := e.pending + key
		e.pending = ""
		switch p {
		case "gg":
			e.row, e.col = 0, 0
		case "dd":
			e.deleteLine()
		case "cc":
			e.pushUndo()
			e.lines[e.row] = nil
			e.col = 0
			e.insert = true
		case "ci":
			e.pending = "ci"
		case "ciw":
			e.changeInnerWord()
		}
		return false
	}
	switch key {
	case "esc":
		return true
	case "g":
		e.pending = "g"
	case "d":
		e.pending = "d"
	case "c":
		e.pending = "c"
	case "h", "left":
		if e.col > 0 {
			e.col--
		}
	case "l", "right":
		if e.col < e.maxCol() {
			e.col++
		}
	case "j", "down":
		if e.row < len(e.lines)-1 {
			e.row++
			e.clampCol()
		}
	case "k", "up":
		if e.row > 0 {
			e.row--
			e.clampCol()
		}
	case "0":
		e.col = 0
	case "^":
		e.col = e.firstNonSpace()
	case "$":
		e.col = e.maxCol()
	case "w":
		e.motionW()
	case "b":
		e.motionB()
	case "e":
		e.motionE()
	case "G":
		e.row = len(e.lines) - 1
		e.clampCol()
	case "x":
		if l := e.line(); e.col < len(l) {
			e.pushUndo()
			e.lines[e.row] = append(l[:e.col], l[e.col+1:]...)
			e.clampCol()
		}
	case "i":
		e.pushUndo()
		e.insert = true
	case "a":
		e.pushUndo()
		e.insert = true
		if len(e.line()) > 0 {
			e.col++
		}
	case "A":
		e.pushUndo()
		e.insert = true
		e.col = len(e.line())
	case "I":
		e.pushUndo()
		e.insert = true
		e.col = e.firstNonSpace()
	case "o":
		e.pushUndo()
		e.insertLineAt(e.row+1, nil)
		e.row++
		e.col = 0
		e.insert = true
	case "O":
		e.pushUndo()
		e.insertLineAt(e.row, nil)
		e.col = 0
		e.insert = true
	case "p":
		if e.hasReg {
			e.pushUndo()
			e.insertLineAt(e.row+1, append([]rune{}, e.reg...))
			e.row++
			e.clampCol()
		}
	case "u":
		e.popUndo()
	case "enter":
		if e.row < len(e.lines)-1 {
			e.row++
			e.col = e.firstNonSpace()
			e.clampCol()
		}
	}
	return false
}

func (e *Editor) deleteLine() {
	e.pushUndo()
	e.reg = append([]rune{}, e.line()...)
	e.hasReg = true
	if len(e.lines) == 1 {
		e.lines[0] = nil
	} else {
		e.lines = append(e.lines[:e.row], e.lines[e.row+1:]...)
		if e.row >= len(e.lines) {
			e.row = len(e.lines) - 1
		}
	}
	e.clampCol()
}

// changeInnerWord deletes the word (or space run) under the cursor and
// enters insert mode, like vim's ciw.
func (e *Editor) changeInnerWord() {
	e.pushUndo()
	l := e.line()
	if len(l) == 0 {
		e.insert = true
		return
	}
	col := clamp(e.col, 0, len(l)-1)
	c := charClass(l[col])
	lo, hi := col, col
	for lo > 0 && charClass(l[lo-1]) == c {
		lo--
	}
	for hi+1 < len(l) && charClass(l[hi+1]) == c {
		hi++
	}
	e.lines[e.row] = append(append([]rune{}, l[:lo]...), l[hi+1:]...)
	e.col = lo
	e.insert = true
}

// charClass groups runes for word motions: space, word or punctuation.
func charClass(r rune) int {
	switch {
	case r == ' ' || r == '\t':
		return 0
	case r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9'):
		return 1
	}
	return 2
}

func (e *Editor) motionW() {
	l := e.line()
	if e.col < len(l) {
		c := charClass(l[e.col])
		for e.col < len(l) && charClass(l[e.col]) == c {
			e.col++
		}
	}
	for {
		l = e.line()
		for e.col < len(l) && charClass(l[e.col]) == 0 {
			e.col++
		}
		if e.col < len(l) || e.row == len(e.lines)-1 {
			break
		}
		e.row++
		e.col = 0
		if len(e.line()) == 0 {
			break // vim stops on empty lines
		}
	}
	e.clampCol()
}

func (e *Editor) motionB() {
	// step left, crossing to the end of the previous line
	step := func() bool {
		if e.col > 0 {
			e.col--
			return true
		}
		if e.row > 0 {
			e.row--
			e.col = max(0, len(e.line())-1)
			return true
		}
		return false
	}
	if !step() {
		return
	}
	for len(e.line()) == 0 || (e.col < len(e.line()) && charClass(e.line()[e.col]) == 0) {
		if !step() {
			return
		}
	}
	c := charClass(e.line()[e.col])
	for e.col > 0 && charClass(e.line()[e.col-1]) == c {
		e.col--
	}
}

func (e *Editor) motionE() {
	step := func() bool {
		if e.col < len(e.line())-1 {
			e.col++
			return true
		}
		if e.row < len(e.lines)-1 {
			e.row++
			e.col = 0
			return true
		}
		return false
	}
	if !step() {
		return
	}
	for len(e.line()) == 0 || charClass(e.line()[e.col]) == 0 {
		if !step() {
			return
		}
	}
	c := charClass(e.line()[e.col])
	for e.col < len(e.line())-1 && charClass(e.line()[e.col+1]) == c {
		e.col++
	}
}

// View renders the visible window; the cursor is drawn reversed.
func (e *Editor) View(w, h int) string {
	if h < 1 {
		h = 1
	}
	if e.row < e.scroll {
		e.scroll = e.row
	}
	if e.row >= e.scroll+h {
		e.scroll = e.row - h + 1
	}
	e.scroll = clamp(e.scroll, 0, max(0, len(e.lines)-1))
	cur := lipgloss.NewStyle().Reverse(true)
	var b strings.Builder
	for i := 0; i < h; i++ {
		li := e.scroll + i
		switch {
		case li >= len(e.lines):
			b.WriteString(stDim.Render("~"))
		case li == e.row:
			l := e.lines[li]
			col := clamp(e.col, 0, len(l))
			start := 0
			if w > 2 && col > w-2 {
				start = col - (w - 2)
			}
			left := string(l[start:col])
			cursor, rest := " ", ""
			if col < len(l) {
				cursor = string(l[col])
				rest = string(l[col+1:])
			}
			rest = truncate(rest, max(0, w-len([]rune(left))-1))
			b.WriteString(stContext.Render(left) + cur.Render(cursor) + stContext.Render(rest))
		default:
			b.WriteString(stContext.Render(truncate(string(e.lines[li]), w)))
		}
		if i < h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
