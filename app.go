package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type viewKind int

const (
	viewLocal viewKind = iota
	viewCommits
	viewPR
	viewFiles
	viewCount
)

type focusArea int

const (
	focusTree focusArea = iota
	focusDiff
)

type localMsg struct {
	files []*FileEntry
	err   error
}

type prMsg struct {
	files []*FileEntry
	info  *PRInfo
	err   error
}

type commitsMsg struct {
	commits []*Commit
	err     error
}

type filesMsg struct {
	files []*FileEntry
	err   error
}

type editorDoneMsg struct{}

// defaultContext keeps hunks small; git's default of 3 merges nearby
// changes into large hunks.
const defaultContext = 1

type App struct {
	root  string
	store *Store

	view  viewKind
	focus focusArea
	full  bool
	w, h  int

	localFiles []*FileEntry
	localErr   string
	prFiles    []*FileEntry
	prInfo     *PRInfo
	prLoaded   bool
	prLoading  bool
	prErr      string

	commits        []*Commit
	commitsLoaded  bool
	commitsLoading bool
	commitsErr     string

	allFiles     []*FileEntry
	filesLoaded  bool
	filesLoading bool
	filesErr     string

	localTree  *TreeModel
	prTree     *TreeModel
	filesTree  *TreeModel
	commitList *CommitList
	commitOpen *Commit    // commit drilled into via enter, nil = list
	commitTree *TreeModel // file tree of commitOpen
	diff       *DiffView

	status   string
	showHelp bool
	context  int // -U<n> context lines around changes
	cfg      Config

	markOpen bool  // mark popup (m) is showing
	markNode *Node // file or folder the popup is about
	markSel  int   // selected option

	reviewOpen bool    // review popup (i) is showing
	reviewRef  string  // selection ref captured when the popup opened
	editor     *Editor // inline editor for the review document

	zPending bool // first z of a zz chord seen

	searching   bool   // search bar open, typing
	searchInput string // text being typed
	searchQuery string // active query (highlights, n/N)
}

func NewApp(root string, store *Store) (*App, error) {
	a := &App{root: root, store: store, context: defaultContext}
	a.diff = &DiffView{store: store, syntax: true}
	files, err := loadLocal(root, a.context)
	if err != nil {
		a.localErr = err.Error()
	}
	a.applyExcludes(files)
	a.localFiles = files
	a.localTree = NewTree(files, nil)
	a.syncDiff()
	return a, nil
}

func (a *App) Init() tea.Cmd { return nil }

func loadLocalCmd(root string, ctx int) tea.Cmd {
	return func() tea.Msg {
		files, err := loadLocal(root, ctx)
		return localMsg{files, err}
	}
}

func loadPRCmd(root string, ctx int) tea.Cmd {
	return func() tea.Msg {
		files, info, err := loadPR(root, ctx)
		return prMsg{files, info, err}
	}
}

func loadCommitsCmd(root string, ctx int) tea.Cmd {
	return func() tea.Msg {
		commits, err := loadCommits(root, ctx)
		return commitsMsg{commits, err}
	}
}

func loadFilesCmd(root string) tea.Cmd {
	return func() tea.Msg {
		files, err := loadFiles(root)
		return filesMsg{files, err}
	}
}

func rebuildTree(old *TreeModel, files []*FileEntry) *TreeModel {
	var exp map[string]bool
	cur := ""
	if old != nil {
		exp = old.ExpandedMap()
		if n := old.Current(); n != nil {
			cur = n.Path
		}
	}
	t := NewTree(files, exp)
	if cur != "" {
		t.SelectPath(cur)
	}
	return t
}

// applyExcludes re-reads the config (so edits take effect on reload) and
// flags matching files.
func (a *App) applyExcludes(files []*FileEntry) {
	a.cfg = loadConfig(a.root)
	for _, f := range files {
		f.Excluded = a.cfg.Excluded(f.Path)
	}
}

func (a *App) activeTree() *TreeModel {
	switch a.view {
	case viewPR:
		return a.prTree
	case viewCommits:
		return a.commitTree // nil while the commit list is shown
	case viewFiles:
		return a.filesTree
	}
	return a.localTree
}

// syncDiff points the right pane at the file (or commit) under the cursor.
func (a *App) syncDiff() {
	if a.view == viewCommits && a.commitOpen == nil {
		if a.commitList == nil || a.commitList.Current() == nil {
			a.diff.SetEntry(nil)
		} else {
			a.diff.SetEntry(a.commitList.Current().Entry())
		}
		return
	}
	t := a.activeTree()
	if t == nil {
		a.diff.SetEntry(nil)
		return
	}
	n := t.Current()
	if n == nil || n.IsDir {
		a.diff.SetEntry(nil)
		return
	}
	if a.view == viewFiles {
		ensurePreview(a.root, n.Entry)
	}
	a.diff.SetEntry(n.Entry)
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
		return a, nil
	case localMsg:
		if msg.err != nil {
			a.localErr = msg.err.Error()
			return a, nil
		}
		a.localErr = ""
		a.applyExcludes(msg.files)
		a.localFiles = msg.files
		a.localTree = rebuildTree(a.localTree, msg.files)
		if a.view == viewLocal {
			a.syncDiff()
		}
		return a, nil
	case prMsg:
		a.prLoading = false
		a.prLoaded = true
		if msg.err != nil {
			a.prErr = msg.err.Error()
			return a, nil
		}
		a.prErr = ""
		a.applyExcludes(msg.files)
		a.prFiles = msg.files
		a.prInfo = msg.info
		a.prTree = rebuildTree(a.prTree, msg.files)
		if a.view == viewPR {
			a.syncDiff()
		}
		return a, nil
	case commitsMsg:
		a.commitsLoading = false
		a.commitsLoaded = true
		if msg.err != nil {
			a.commitsErr = msg.err.Error()
			return a, nil
		}
		a.commitsErr = ""
		for _, c := range msg.commits {
			a.applyExcludes(c.Files)
		}
		a.commits = msg.commits
		a.commitList = rebuildCommitList(a.commitList, msg.commits)
		if a.commitOpen != nil {
			var found *Commit
			for _, c := range msg.commits {
				if c.Hash == a.commitOpen.Hash {
					found = c
					break
				}
			}
			a.commitOpen = found
			if found != nil {
				a.commitTree = rebuildTree(a.commitTree, found.Files)
			} else {
				a.commitTree = nil
			}
		}
		if a.view == viewCommits {
			a.syncDiff()
		}
		return a, nil
	case filesMsg:
		a.filesLoading = false
		a.filesLoaded = true
		if msg.err != nil {
			a.filesErr = msg.err.Error()
			return a, nil
		}
		a.filesErr = ""
		a.applyExcludes(msg.files)
		a.allFiles = msg.files
		first := a.filesTree == nil
		a.filesTree = rebuildTree(a.filesTree, msg.files)
		if first {
			a.filesTree.CollapseAll("app")
		}
		if a.view == viewFiles {
			a.syncDiff()
		}
		return a, nil
	case editorDoneMsg:
		return a, a.refreshCmd()
	case tea.MouseMsg:
		return a.handleMouse(msg)
	case tea.KeyMsg:
		return a.handleKey(msg)
	}
	return a, nil
}

// handleMouse scrolls the pane under the pointer with the mouse wheel:
// one row per tick in the lists, three in the diff.
func (a *App) handleMouse(m tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.Action != tea.MouseActionPress {
		return a, nil
	}
	sign := 0
	switch m.Button {
	case tea.MouseButtonWheelUp:
		sign = -1
	case tea.MouseButtonWheelDown:
		sign = 1
	default:
		return a, nil
	}
	overTree := a.focus == focusTree
	if !a.full {
		tw := clamp(a.w/3, 24, 48)
		if tw > a.w-20 {
			tw = a.w / 2
		}
		overTree = m.X < tw
	}
	if overTree {
		if a.view == viewCommits {
			if a.commitList != nil {
				a.commitList.Move(sign)
				a.syncDiff()
			}
		} else if t := a.activeTree(); t != nil {
			t.Move(sign)
			a.syncDiff()
		}
	} else {
		a.diff.Scroll(sign * 3)
	}
	return a, nil
}

func (a *App) refreshCmd() tea.Cmd {
	switch a.view {
	case viewPR:
		a.prLoading = true
		return loadPRCmd(a.root, a.context)
	case viewCommits:
		a.commitsLoading = true
		return loadCommitsCmd(a.root, a.context)
	case viewFiles:
		a.filesLoading = true
		return loadFilesCmd(a.root)
	}
	return loadLocalCmd(a.root, a.context)
}

func (a *App) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	a.status = ""
	if a.searching {
		return a.handleSearchKey(msg, key)
	}
	if a.showHelp {
		if key == "ctrl+c" {
			return a, tea.Quit
		}
		a.showHelp = false
		return a, nil
	}
	if a.markOpen {
		return a.handleMarkKey(key)
	}
	if a.reviewOpen {
		if key == "ctrl+c" {
			return a, tea.Quit
		}
		if key == "enter" && !a.editor.insert {
			a.insertReviewRef()
			return a, nil
		}
		if a.editor.HandleKey(msg) {
			a.saveReview()
		}
		return a, nil
	}
	if a.zPending {
		a.zPending = false
		switch key {
		case "z":
			a.alignCursor(0)
		case "t":
			a.alignCursor(-1)
		case "b":
			a.alignCursor(1)
		}
		return a, nil
	}
	if key == "z" {
		a.zPending = true
		return a, nil
	}
	switch key {
	case "ctrl+c", "q":
		return a, tea.Quit
	case "?":
		a.showHelp = true
		return a, nil
	case "J", "ctrl+j":
		a.diff.Scroll(1)
		return a, nil
	case "K", "ctrl+k":
		a.diff.Scroll(-1)
		return a, nil
	case "ctrl+o":
		a.copyReviewPrompt()
		return a, nil
	case "i":
		a.openReview()
		return a, nil
	case "I":
		a.copyReviewDoc()
		return a, nil
	case "ctrl+x":
		a.clearReviewDoc()
		return a, nil
	case "H":
		a.diff.syntax = !a.diff.syntax
		if a.diff.syntax {
			a.status = "syntax highlighting: everything"
		} else {
			a.status = "syntax highlighting: selection only"
		}
		return a, nil
	case "ctrl+w":
		a.diff.smart = !a.diff.smart
		a.diff.rebuild()
		if a.diff.smart {
			a.status = "clean diff: whitespace-only hunks hidden, word-level changes"
		} else {
			a.status = "clean diff off"
		}
		return a, nil
	case "ctrl+d", "pgdown":
		a.diff.Scroll(max(1, a.diff.h/2))
		return a, nil
	case "ctrl+u", "pgup":
		a.diff.Scroll(-max(1, a.diff.h/2))
		return a, nil
	case "{":
		return a.setContext(a.context - 1)
	case "}":
		return a.setContext(a.context + 1)
	case "[":
		return a.switchView((a.view + viewCount - 1) % viewCount)
	case "]":
		return a.switchView((a.view + 1) % viewCount)
	case "1":
		return a.switchView(viewLocal)
	case "2":
		return a.switchView(viewCommits)
	case "3":
		return a.switchView(viewPR)
	case "4":
		return a.switchView(viewFiles)
	case "+", "=":
		a.full = !a.full
		return a, nil
	case "e":
		return a, a.openEditor()
	case "r":
		return a, a.refreshCmd()
	case "/":
		a.searching = true
		a.searchInput = ""
		return a, nil
	case "n":
		if a.searchQuery != "" {
			a.searchJump(1)
			return a, nil
		}
	case "N":
		if a.searchQuery != "" {
			a.searchJump(-1)
			return a, nil
		}
	case "esc":
		if a.searchQuery != "" {
			a.clearSearch()
			return a, nil
		}
	}
	if a.focus == focusTree {
		if a.view == viewCommits && a.commitOpen == nil {
			return a.handleCommitsKey(key)
		}
		return a.handleTreeKey(key)
	}
	return a.handleDiffKey(key)
}

func (a *App) handleCommitsKey(key string) (tea.Model, tea.Cmd) {
	cl := a.commitList
	if cl == nil {
		return a, nil
	}
	switch key {
	case "j", "down":
		cl.Move(1)
		a.syncDiff()
	case "k", "up":
		cl.Move(-1)
		a.syncDiff()
	case "g", "<":
		cl.cursor = 0
		a.syncDiff()
	case "G", ">":
		if len(cl.commits) > 0 {
			cl.cursor = len(cl.commits) - 1
		}
		a.syncDiff()
	case "enter", "l":
		if c := cl.Current(); c != nil {
			a.commitOpen = c
			a.commitTree = NewTree(c.Files, nil)
			a.syncDiff()
		}
	case " ", "space":
		if c := cl.Current(); c != nil {
			if m := c.ToggleReviewed(a.store, false); m != "" {
				a.status = m
			}
		}
	case "S", "ctrl+@":
		if c := cl.Current(); c != nil {
			if m := c.ToggleReviewed(a.store, true); m != "" {
				a.status = m
			}
		}
	}
	return a, nil
}

func (a *App) switchView(v viewKind) (tea.Model, tea.Cmd) {
	a.view = v
	a.focus = focusTree
	var cmd tea.Cmd
	if v == viewPR && !a.prLoaded && !a.prLoading {
		a.prLoading = true
		cmd = loadPRCmd(a.root, a.context)
	}
	if v == viewCommits && !a.commitsLoaded && !a.commitsLoading {
		a.commitsLoading = true
		cmd = loadCommitsCmd(a.root, a.context)
	}
	if v == viewFiles && !a.filesLoaded && !a.filesLoading {
		a.filesLoading = true
		cmd = loadFilesCmd(a.root)
	}
	a.syncDiff()
	return a, cmd
}

func (a *App) handleSearchKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c":
		return a, tea.Quit
	case "enter":
		a.searching = false
		a.searchQuery = a.searchInput
		if a.searchQuery == "" {
			a.clearSearch()
		}
	case "esc":
		a.searching = false
		a.clearSearch()
	case "backspace":
		r := []rune(a.searchInput)
		if len(r) > 0 {
			a.searchInput = string(r[:len(r)-1])
		}
		a.searchQuery = a.searchInput
		a.liveSearch()
	case " ", "space":
		a.searchInput += " "
		a.searchQuery = a.searchInput
		a.liveSearch()
	default:
		if msg.Type == tea.KeyRunes {
			a.searchInput += string(msg.Runes)
			a.searchQuery = a.searchInput
			a.liveSearch()
		}
	}
	return a, nil
}

func (a *App) clearSearch() {
	a.searchInput = ""
	a.searchQuery = ""
	a.diff.searchRow = -1
}

// liveSearch keeps the cursor on a match while typing: stay if the current
// position already matches, otherwise jump to the next match.
func (a *App) liveSearch() {
	q := a.searchQuery
	if q == "" {
		a.diff.searchRow = -1
		return
	}
	lq := strings.ToLower(q)
	switch {
	case a.focus == focusDiff:
		d := a.diff
		d.searchRow = -1
		if len(d.sels) > 0 {
			row := d.sels[d.cursor]
			if strings.Contains(strings.ToLower(d.rows[row].text), lq) {
				d.searchRow = row
				return
			}
		}
		d.SearchJump(q, 1)
	case a.view == viewCommits:
		if a.commitList == nil {
			return
		}
		if c := a.commitList.Current(); c != nil &&
			strings.Contains(strings.ToLower(c.Short()+" "+c.Subject), lq) {
			return
		}
		if a.commitList.SearchJump(q, 1) {
			a.syncDiff()
		}
	default:
		t := a.activeTree()
		if t == nil {
			return
		}
		if n := t.Current(); n != nil && strings.Contains(strings.ToLower(n.Path), lq) {
			return
		}
		if t.SearchJump(q, 1) {
			a.syncDiff()
		}
	}
}

func (a *App) searchJump(dir int) {
	q := a.searchQuery
	switch {
	case a.focus == focusDiff:
		if !a.diff.SearchJump(q, dir) {
			a.status = "no match: " + q
		}
	case a.view == viewCommits:
		if a.commitList == nil || !a.commitList.SearchJump(q, dir) {
			a.status = "no match: " + q
		} else {
			a.syncDiff()
		}
	default:
		t := a.activeTree()
		if t == nil || !t.SearchJump(q, dir) {
			a.status = "no match: " + q
		} else {
			a.syncDiff()
		}
	}
}

// setContext changes the -U<n> width and reloads every loaded view so
// hunk boundaries are recomputed. Review marks are unaffected: line IDs
// hash only changed lines, never context.
func (a *App) setContext(n int) (tea.Model, tea.Cmd) {
	n = clamp(n, 0, 99)
	if n == a.context {
		a.status = fmt.Sprintf("context: %d lines", n)
		return a, nil
	}
	a.context = n
	a.status = fmt.Sprintf("context: %d lines", n)
	cmds := []tea.Cmd{loadLocalCmd(a.root, n)}
	if a.prLoaded || a.prLoading {
		a.prLoading = true
		cmds = append(cmds, loadPRCmd(a.root, n))
	}
	if a.commitsLoaded || a.commitsLoading {
		a.commitsLoading = true
		cmds = append(cmds, loadCommitsCmd(a.root, n))
	}
	return a, tea.Batch(cmds...)
}

func (a *App) handleTreeKey(key string) (tea.Model, tea.Cmd) {
	t := a.activeTree()
	if t == nil {
		return a, nil
	}
	switch key {
	case "j", "down":
		t.Move(1)
		a.syncDiff()
	case "k", "up":
		t.Move(-1)
		a.syncDiff()
	case "g", "<":
		t.cursor = 0
		a.syncDiff()
	case "G", ">":
		if len(t.rows) > 0 {
			t.cursor = len(t.rows) - 1
		}
		a.syncDiff()
	case "enter", "l":
		n := t.Current()
		if n == nil {
			break
		}
		if n.IsDir {
			n.Expanded = !n.Expanded
			t.flatten()
		} else {
			a.focus = focusDiff
		}
	case "h":
		n := t.Current()
		if n == nil {
			break
		}
		if n.IsDir && n.Expanded {
			n.Expanded = false
			t.flatten()
		} else if dir := filepath.Dir(n.Path); dir != "." {
			t.SelectPath(dir)
			a.syncDiff()
		}
	case " ", "space", "S", "ctrl+@":
		n := t.Current()
		if n == nil {
			break
		}
		if n.IsDir {
			a.status = "select a file to toggle its review state"
			break
		}
		if a.view == viewFiles {
			a.toggleWholeFile(n.Entry, key != " " && key != "space")
			break
		}
		if m := n.Entry.ToggleAllReviewed(a.store, key != " " && key != "space"); m != "" {
			a.status = m
		}
	case "m":
		if n := t.Current(); n != nil {
			a.markOpen = true
			a.markNode = n
			a.markSel = 0
		}
	case "esc":
		if a.view == viewCommits && a.commitOpen != nil {
			a.commitOpen = nil
			a.commitTree = nil
			a.syncDiff()
		}
	case "s":
		if a.view != viewLocal {
			a.status = "staging only works in the LOCAL view"
			break
		}
		n := t.Current()
		if n == nil {
			break
		}
		action := "staged "
		var err error
		if anyStagedUnder(n) {
			action = "unstaged "
			err = unstagePath(a.root, n.Path)
		} else {
			err = stagePath(a.root, n.Path)
		}
		if err != nil {
			a.status = err.Error()
			break
		}
		a.status = action + n.Path
		return a, loadLocalCmd(a.root, a.context)
	}
	return a, nil
}

// alignCursor scrolls the focused pane so the cursor row sits at the
// top (zt), middle (zz) or bottom (zb). The list panes render their
// content h-4 rows tall (border, title, status bar).
func (a *App) alignCursor(pos int) {
	if a.focus == focusDiff {
		a.diff.Align(pos)
		return
	}
	h := max(1, a.h-4)
	if a.view == viewCommits && a.commitOpen == nil {
		if a.commitList != nil {
			a.commitList.Align(h, pos)
		}
		return
	}
	if t := a.activeTree(); t != nil {
		t.Align(h, pos)
	}
}

// wholeFileIDs returns every ID a FILES-view mark covers. Marking a file
// means "the version on disk is reviewed", which covers every change
// that led to it: the line IDs of all commits touching the file plus the
// current local (and loaded PR/commit) diff lines, alongside the file
// ID. Later changes hash to new IDs and stay unreviewed.
func (a *App) wholeFileIDs(e *FileEntry) []string {
	ids := append([]string{e.FileID}, fileHistoryIDs(a.root, e.Path)...)
	byPath := func(files []*FileEntry) *FileEntry {
		for _, f := range files {
			if f.Path == e.Path {
				return f
			}
		}
		return nil
	}
	ids = append(ids, entryIDs(byPath(a.localFiles))...)
	ids = append(ids, entryIDs(byPath(a.prFiles))...)
	for _, c := range a.commits {
		ids = append(ids, entryIDs(byPath(c.Files))...)
	}
	return ids
}

// toggleWholeFile flips the FILES-view mark for a file.
func (a *App) toggleWholeFile(e *FileEntry, skim bool) {
	if e == nil || e.FileID == "" {
		return
	}
	if e.Excluded {
		a.status = "file is excluded from review (.revu/config.json)"
		return
	}
	v := !a.store.In(e.FileID, skim)
	for _, id := range a.wholeFileIDs(e) {
		a.store.Mark(id, skim, v)
	}
	if err := a.store.Save(); err != nil {
		a.status = "failed to save review state: " + err.Error()
	}
}

// markAllIn reports whether every markable unit under the node already
// carries the given mark (and whether there is anything markable).
func (a *App) markAllIn(n *Node, skim bool) bool {
	any := false
	for _, e := range entriesUnder(n) {
		if e.Excluded {
			continue
		}
		if a.view == viewFiles {
			if e.FileID == "" {
				continue
			}
			any = true
			if !a.store.In(e.FileID, skim) {
				return false
			}
			continue
		}
		for _, id := range reviewableIDs(e) {
			any = true
			if !a.store.In(id, skim) {
				return false
			}
		}
	}
	return any
}

// applyMark executes a mark-popup choice for a file or folder node:
// 0 reviewed, 1 skimmed (content-addressed, like space/S on a file),
// 2/3 the permanent variants (path-based, survive content changes).
// Choosing an already-active option removes the mark again. Content
// marks layer on top of a permanent mark without removing it — manual
// overrides never drop the file's permanent marking.
func (a *App) applyMark(n *Node, opt int) {
	if n == nil {
		return
	}
	skim := opt == 1 || opt == 3
	if opt >= 2 {
		a.store.SetPermanent(n.Path, skim, !a.store.PermanentAt(n.Path, skim))
	} else {
		v := !a.markAllIn(n, skim)
		var ids []string
		for _, e := range entriesUnder(n) {
			if e.Excluded {
				continue
			}
			if a.view == viewFiles {
				ids = append(ids, a.wholeFileIDs(e)...)
			} else {
				ids = append(ids, reviewableIDs(e)...)
			}
		}
		if len(ids) == 0 {
			a.status = "nothing markable here (only staged changes count)"
			return
		}
		for _, id := range ids {
			a.store.Mark(id, skim, v)
		}
	}
	if err := a.store.Save(); err != nil {
		a.status = "failed to save review state: " + err.Error()
	}
}

var markOptions = []struct{ label, desc string }{
	{"reviewed", "expires when the content changes"},
	{"skimmed", "read over; expires when the content changes"},
	{"permanently reviewed", "for generated files/dirs, implicitly reviewed; survives any change"},
	{"permanently skimmed", "for files/dirs with limited impact, safe to approve; survives any change"},
}

func (a *App) handleMarkKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c":
		return a, tea.Quit
	case "esc", "q", "m":
		a.markOpen = false
	case "j", "down":
		a.markSel = (a.markSel + 1) % len(markOptions)
	case "k", "up":
		a.markSel = (a.markSel + len(markOptions) - 1) % len(markOptions)
	case "enter", " ", "space":
		a.markOpen = false
		a.applyMark(a.markNode, a.markSel)
	}
	return a, nil
}

// markState returns the option currently in effect for the node, -1 for
// none. Exactly one state is reported: fully explicit content marks win,
// otherwise a permanent mark shows as the active default.
func (a *App) markState(n *Node) int {
	switch {
	case a.markAllIn(n, true):
		return 1
	case a.markAllIn(n, false):
		return 0
	case a.store.PermanentAt(n.Path, true):
		return 3
	case a.store.PermanentAt(n.Path, false):
		return 2
	}
	return -1
}

func (a *App) markView() string {
	n := a.markNode
	name := n.Path
	if n.IsDir {
		name += "/"
	}
	state := a.markState(n)
	var b strings.Builder
	for i, o := range markOptions {
		cursor, st := "  ", stContext
		if i == a.markSel {
			cursor, st = "▸ ", stTitle
		}
		check := "  "
		if i == state {
			check = "✓ "
		}
		b.WriteString(cursor + stReviewed.Render(check) + st.Render(padRight(o.label, 22)) +
			stDim.Render(o.desc) + "\n")
	}
	b.WriteString("\n" + stDim.Render("j/k select · enter confirm · esc cancel"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorderF).
		Padding(1, 3).
		Render(stTitle.Render("mark "+truncate(name, 48)) + "\n\n" + b.String())
}

func anyStagedUnder(n *Node) bool {
	if !n.IsDir {
		return n.Entry != nil && n.Entry.Staged
	}
	for _, c := range n.Children {
		if anyStagedUnder(c) {
			return true
		}
	}
	return false
}

func (a *App) handleDiffKey(key string) (tea.Model, tea.Cmd) {
	d := a.diff
	switch key {
	case "esc":
		if d.visual {
			d.visual = false
			if d.lineMode {
				d.ToggleMode() // drop back to hunk mode
			}
		} else {
			a.focus = focusTree
		}
	case "j", "down":
		d.Move(1)
	case "k", "up":
		d.Move(-1)
	case "g", "<":
		d.Top()
	case "G", ">":
		d.Bottom()
	case "a":
		d.ToggleMode()
	case "v":
		d.StartVisual()
	case " ", "space", "S", "ctrl+@":
		if a.view == viewFiles && d.entry != nil && d.entry.FileID != "" {
			a.toggleWholeFile(d.entry, key != " " && key != "space")
			break
		}
		if m := d.ToggleReviewed(key != " " && key != "space"); m != "" {
			a.status = m
		}
	case "s":
		return a.stageSelection()
	}
	return a, nil
}

// copyToClipboard is a variable so tests can capture the copied text.
var copyToClipboard = func(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	default:
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		} else {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		}
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// copyReviewPrompt puts a review prompt for the selected file on the
// clipboard; from the diff pane it includes the selected line range.
func (a *App) copyReviewPrompt() {
	var path, lines string
	if a.focus == focusDiff && a.diff.entry != nil {
		path = a.diff.CurrentFilePath()
		if lo, hi := a.diff.SelectionRange(); lo > 0 {
			if lo == hi {
				lines = fmt.Sprintf("%d", lo)
			} else {
				lines = fmt.Sprintf("%d-%d", lo, hi)
			}
		}
	} else if t := a.activeTree(); t != nil {
		if n := t.Current(); n != nil && !n.IsDir {
			path = n.Path
		}
	}
	if path == "" {
		a.status = "no file selected"
		return
	}
	text := "review diese änderungen:\nDatei: " + path + "\n"
	if lines != "" {
		text += "Zeilen: " + lines + "\n"
	}
	if err := copyToClipboard(text); err != nil {
		a.status = "copy failed: " + err.Error()
		return
	}
	a.status = "copied review prompt for " + path
}

// stageSelection toggles staging for the current hunk, line or visual
// range: unstaged selections are staged via a partial patch to
// `git apply --cached`, staged selections are reverse-applied.
func (a *App) stageSelection() (tea.Model, tea.Cmd) {
	if a.view != viewLocal {
		a.status = "staging only works in the LOCAL view"
		return a, nil
	}
	d := a.diff
	e := d.entry
	if e == nil {
		return a, nil
	}
	if e.Untracked {
		if err := stagePath(a.root, e.Path); err != nil {
			a.status = err.Error()
			return a, nil
		}
		a.status = "staged " + e.Path
		return a, loadLocalCmd(a.root, a.context)
	}
	if len(d.sels) == 0 {
		return a, nil
	}
	staged := map[int]map[int]bool{}
	unstaged := map[int]map[int]bool{}
	addSel := func(selIdx int) {
		r := d.rows[d.sels[selIdx]]
		h := e.Hunks[r.hunk]
		m := unstaged
		if h.Reviewable {
			m = staged
		}
		if m[r.hunk] == nil {
			m[r.hunk] = map[int]bool{}
		}
		if r.kind == rowHunkHeader {
			for _, li := range h.ChangedIdx() {
				m[r.hunk][li] = true
			}
		} else {
			m[r.hunk][r.line] = true
		}
	}
	if !d.lineMode {
		addSel(d.cursor)
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
		for i := lo; i <= hi; i++ {
			addSel(i)
		}
	}
	if len(staged) > 0 && len(unstaged) > 0 {
		a.status = "selection mixes staged and unstaged changes"
		return a, nil
	}
	picksFrom := func(m map[int]map[int]bool) []hunkPick {
		var idxs []int
		for i := range m {
			idxs = append(idxs, i)
		}
		sort.Ints(idxs)
		var picks []hunkPick
		for _, i := range idxs {
			picks = append(picks, hunkPick{hunk: e.Hunks[i], lines: m[i]})
		}
		return picks
	}
	switch {
	case len(unstaged) > 0:
		if err := stageLines(a.root, e.Path, picksFrom(unstaged)); err != nil {
			a.status = "stage failed: " + err.Error()
			return a, nil
		}
		a.status = "staged"
	case len(staged) > 0:
		// partial reverse patches cannot express new/deleted files;
		// unstage those whole via git restore
		if e.Status == 'A' || e.Status == 'D' {
			if err := unstagePath(a.root, e.Path); err != nil {
				a.status = err.Error()
				return a, nil
			}
		} else if err := unstageLines(a.root, e.Path, picksFrom(staged)); err != nil {
			a.status = "unstage failed: " + err.Error()
			return a, nil
		}
		a.status = "unstaged"
	default:
		return a, nil
	}
	d.visual = false
	return a, loadLocalCmd(a.root, a.context)
}

func (a *App) openEditor() tea.Cmd {
	var path string
	line := 0
	if a.focus == focusDiff && a.diff.entry != nil {
		path = a.diff.CurrentFilePath()
		line = a.diff.CurrentLineInFile()
	} else if t := a.activeTree(); t != nil {
		if n := t.Current(); n != nil && !n.IsDir {
			path = n.Path
		}
	}
	if path == "" {
		a.status = "no file selected"
		return nil
	}
	return editorCmd(a.root, filepath.Join(a.root, path), line)
}

// editorCmd opens $EDITOR on the file, jumping to line when supported.
func editorCmd(root, abs string, line int) tea.Cmd {
	ed := os.Getenv("EDITOR")
	if ed == "" {
		ed = "vim"
	}
	parts := strings.Fields(ed)
	name := parts[0]
	args := parts[1:]
	if line > 0 && supportsPlusLine(name) {
		args = append(args, fmt.Sprintf("+%d", line))
	}
	args = append(args, abs)
	c := exec.Command(name, args...)
	c.Dir = root
	return tea.ExecProcess(c, func(error) tea.Msg { return editorDoneMsg{} })
}

// reviewFile is the persistent review document: selection references
// with feedback below each, kept until cleared with ctrl+x.
func (a *App) reviewFile() string {
	return filepath.Join(a.root, ".revu", "review.md")
}

func (a *App) hasReview() bool {
	fi, err := os.Stat(a.reviewFile())
	return err == nil && fi.Size() > 0
}

// selectionRef renders the current selection as "path:lines" — the file
// and line range in the diff pane, the node path in the tree.
func (a *App) selectionRef() string {
	if a.focus == focusDiff && a.diff.entry != nil {
		ref := a.diff.CurrentFilePath()
		if lo, hi := a.diff.SelectionRange(); lo > 0 {
			if lo == hi {
				ref += fmt.Sprintf(":%d", lo)
			} else {
				ref += fmt.Sprintf(":%d-%d", lo, hi)
			}
		}
		return ref
	}
	if t := a.activeTree(); t != nil {
		if n := t.Current(); n != nil {
			return n.Path
		}
	}
	return ""
}

// openReview opens the inline editor on the review document, in normal
// mode with nothing inserted; the current selection reference is only
// captured — enter pastes it (see insertReviewRef).
func (a *App) openReview() {
	a.reviewRef = a.selectionRef()
	old, _ := os.ReadFile(a.reviewFile())
	a.editor = NewEditor(strings.TrimRight(string(old), "\n"), 1<<30, false)
	a.reviewOpen = true
}

// insertReviewRef appends the captured selection reference and puts the
// cursor on the blank feedback line below it, in insert mode.
func (a *App) insertReviewRef() {
	if a.reviewRef == "" {
		return
	}
	e := a.editor
	e.pushUndo()
	text := strings.TrimRight(e.Text(), " \t\n")
	if text != "" {
		text += "\n\n"
	}
	text += a.reviewRef + ":\n" // trailing newline yields the blank feedback line
	e.setText(text)
	e.row = len(e.lines) - 1
	e.col = 0
	e.insert = true
}

// saveReview persists the editor buffer and closes the popup; an empty
// buffer removes the document.
func (a *App) saveReview() {
	a.reviewOpen = false
	text := strings.TrimRight(a.editor.Text(), " \t\n")
	path := a.reviewFile()
	if text == "" {
		_ = os.Remove(path)
		a.status = "review document is empty"
		return
	}
	if err := ensureRevuDir(filepath.Dir(path)); err != nil {
		a.status = err.Error()
		return
	}
	if err := os.WriteFile(path, []byte(text+"\n"), 0o644); err != nil {
		a.status = err.Error()
		return
	}
	a.status = "review saved · I copies it as a prompt"
}

// copyReviewDoc puts the review document on the clipboard, framed as a
// prompt asking to fix all of the feedback.
func (a *App) copyReviewDoc() {
	data, err := os.ReadFile(a.reviewFile())
	doc := strings.TrimSpace(string(data))
	if err != nil || doc == "" {
		a.status = "review document is empty (press i to add feedback)"
		return
	}
	text := "Behebe das folgende Review-Feedback. Jeder Abschnitt beginnt mit" +
		" der Stelle als `datei:zeilen:`, darunter steht das Feedback dazu:\n\n" +
		doc + "\n"
	if err := copyToClipboard(text); err != nil {
		a.status = "copy failed: " + err.Error()
		return
	}
	a.status = "copied review document as prompt"
}

func (a *App) clearReviewDoc() {
	if !a.hasReview() {
		a.status = "review document is already empty"
		return
	}
	if err := os.Remove(a.reviewFile()); err != nil {
		a.status = err.Error()
		return
	}
	a.status = "review document cleared"
}

func supportsPlusLine(ed string) bool {
	switch filepath.Base(ed) {
	case "vim", "nvim", "vi", "nano", "emacs", "micro":
		return true
	}
	return false
}

func (a *App) View() string {
	if a.w == 0 || a.h == 0 {
		return ""
	}
	paneH := a.h - 1
	var body string
	if a.full {
		if a.focus == focusTree {
			body = a.renderTreePane(a.w, paneH)
		} else {
			body = a.renderDiffPane(a.w, paneH)
		}
	} else {
		tw := clamp(a.w/3, 24, 48)
		if tw > a.w-20 {
			tw = a.w / 2
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			a.renderTreePane(tw, paneH),
			a.renderDiffPane(a.w-tw, paneH),
		)
	}
	screen := body + "\n" + a.statusBar()
	switch {
	case a.showHelp:
		return overlay(screen, a.helpView(), a.w, a.h)
	case a.markOpen:
		return overlay(screen, a.markView(), a.w, a.h)
	case a.reviewOpen:
		return overlay(screen, a.reviewView(), a.w, a.h)
	}
	return screen
}

func (a *App) reviewView() string {
	w := clamp(a.w-16, 40, 96)
	h := clamp(a.h-10, 6, 24)
	mode := "NORMAL"
	hints := "esc save & close · hjkl w b e 0 ^ $ gg G · x dd cc ciw p o O · u undo · i insert"
	if a.reviewRef != "" {
		hints = "enter: add " + a.reviewRef + " · " + hints
	}
	if a.editor.insert {
		mode = "INSERT"
		hints = "esc: normal mode"
	}
	title := stTitle.Render("review.md") + stDim.Render(" · "+mode)
	// pad the hint line to the editor width so the box keeps its size
	// when the (shorter) insert-mode hint is shown
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorderF).
		Padding(0, 1).
		Render(title + "\n" + a.editor.View(w, h) + "\n" + stDim.Render(padRight(truncate(hints, w), w)))
}

// viewTabs renders all view names, bracketing the active one.
func (a *App) viewTabs(maxW int) string {
	labels := []string{"LOCAL", "COMMITS", "PR", "FILES"}
	if a.prInfo != nil {
		labels[viewPR] = fmt.Sprintf("PR #%d", a.prInfo.Number)
	}
	render := func() string {
		var parts []string
		for i, l := range labels {
			if viewKind(i) == a.view {
				parts = append(parts, stTitle.Render("["+l+"]"))
			} else {
				parts = append(parts, stDim.Render(l))
			}
		}
		return strings.Join(parts, stDim.Render(" · "))
	}
	tabs := render()
	if lipgloss.Width(tabs) > maxW {
		labels[viewPR] = "PR"
		tabs = render()
	}
	return tabs
}

// reviewProgress sums reviewed/total reviewable lines for the active view.
func (a *App) reviewProgress() (int, int) {
	sum := func(files []*FileEntry) (int, int) {
		rev, tot := 0, 0
		for _, f := range files {
			r, t := f.Counts(a.store)
			rev += r
			tot += t
		}
		return rev, tot
	}
	switch a.view {
	case viewPR:
		return sum(a.prFiles)
	case viewCommits:
		if a.commitOpen != nil {
			return a.commitOpen.Counts(a.store)
		}
		rev, tot := 0, 0
		for _, c := range a.commits {
			r, t := c.Counts(a.store)
			rev += r
			tot += t
		}
		return rev, tot
	case viewFiles:
		return sum(a.allFiles)
	}
	return sum(a.localFiles)
}

func (a *App) renderTreePane(w, h int) string {
	innerW, innerH := w-2, h-2
	title := a.viewTabs(innerW)
	if a.view == viewCommits && a.commitOpen != nil {
		crumb := stDim.Render(" · " + a.commitOpen.Short())
		if lipgloss.Width(title)+lipgloss.Width(crumb) <= innerW {
			title += crumb
		}
	}
	if rev, tot := a.reviewProgress(); tot > 0 {
		pst := stDim
		switch {
		case rev == tot:
			pst = stReviewed
		case rev > 0:
			pst = stPartial
		}
		pct := pst.Render(fmt.Sprintf("%d%%", rev*100/tot))
		if gap := innerW - lipgloss.Width(title) - lipgloss.Width(pct); gap >= 1 {
			title += strings.Repeat(" ", gap) + pct
		}
	}
	var content string
	switch {
	case a.view == viewLocal && a.localErr != "":
		content = stStatusMsg.Render(truncate(a.localErr, innerW))
	case a.view == viewLocal && len(a.localFiles) == 0:
		content = stDim.Render("(working tree clean)")
	case a.view == viewPR && a.prLoading:
		content = stDim.Render("loading PR…")
	case a.view == viewPR && a.prErr != "":
		content = stStatusMsg.Render(truncate(a.prErr, innerW))
	case a.view == viewPR && a.prTree == nil:
		content = stDim.Render("(no PR data)")
	case a.view == viewCommits && a.commitsLoading:
		content = stDim.Render("loading commits…")
	case a.view == viewCommits && a.commitsErr != "":
		content = stStatusMsg.Render(truncate(a.commitsErr, innerW))
	case a.view == viewCommits && a.commitList == nil:
		content = stDim.Render("(no commits)")
	case a.view == viewFiles && a.filesLoading:
		content = stDim.Render("loading files…")
	case a.view == viewFiles && a.filesErr != "":
		content = stStatusMsg.Render(truncate(a.filesErr, innerW))
	case a.view == viewFiles && a.filesTree == nil:
		content = stDim.Render("(no files)")
	case a.view == viewCommits && a.commitOpen == nil:
		content = a.commitList.View(innerW, innerH-1, a.store, a.focus == focusTree, a.searchQuery)
	default:
		content = a.activeTree().View(innerW, innerH-1, a.store, a.focus == focusTree, a.searchQuery)
	}
	return pane(title, content, w, h, a.focus == focusTree)
}

func (a *App) renderDiffPane(w, h int) string {
	innerW, innerH := w-2, h-2
	title := "diff"
	if a.diff.entry != nil {
		e := a.diff.entry
		rev, tot := e.Counts(a.store)
		mode := "hunk"
		if a.diff.lineMode {
			mode = "line"
		}
		if a.diff.visual {
			mode = "visual"
		}
		if e.FileID != "" {
			mode = "file"
		}
		extra := ""
		if tot > 0 {
			extra = fmt.Sprintf(" · %d/%d reviewed", rev, tot)
		}
		title = fmt.Sprintf("%s · %s%s", e.Path, mode, extra)
	}
	title = stTitle.Render(truncate(title, innerW))
	var content string
	switch {
	case a.view == viewPR && a.prLoading:
		content = stDim.Render("loading PR…")
	case a.view == viewCommits && a.commitsLoading:
		content = stDim.Render("loading commits…")
	case a.view == viewFiles && a.filesLoading:
		content = stDim.Render("loading files…")
	default:
		content = a.diff.View(innerW, innerH-1, a.focus == focusDiff, a.searchQuery)
	}
	return pane(title, content, w, h, a.focus == focusDiff)
}

// pane wraps a pre-rendered title line and content in a border.
func pane(title, content string, w, h int, focused bool) string {
	innerW, innerH := w-2, h-2
	bc := colBorderU
	if focused {
		bc = colBorderF
	}
	body := title + "\n" + content
	lines := strings.Split(body, "\n")
	if len(lines) > innerH {
		lines = lines[:innerH]
	}
	body = strings.Join(lines, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(bc).
		Width(innerW).
		Height(innerH).
		Render(body)
}

func (a *App) helpView() string {
	type bind struct{ k, d string }
	sections := []struct {
		title string
		binds []bind
	}{
		{"Global", []bind{
			{"[ / ]", "cycle views (LOCAL / COMMITS / PR / FILES)"},
			{"1 / 2 / 3 / 4", "jump to a view directly"},
			{"J / K", "scroll the diff pane (from anywhere)"},
			{"ctrl+d / ctrl+u", "half-page scroll the diff (from anywhere)"},
			{"ctrl+o", "copy review prompt (file; + line range in diff)"},
			{"i", "open the review doc; enter pastes the selection ref"},
			{"I", "copy the review doc as a fix-it prompt"},
			{"ctrl+x", "clear the review doc"},
			{"H", "toggle syntax highlighting (everything ↔ selection only)"},
			{"ctrl+w", "clean diff: hide ws-only hunks, word-level changes"},
			{"{ / }", "shrink / grow diff context by one line"},
			{"/", "search (enter: confirm, esc: cancel)"},
			{"n / N", "next / previous search match"},
			{"< / >", "jump to top / bottom"},
			{"z z / z t / z b", "current line / selection to center, top, bottom"},
			{"+", "toggle fullscreen for the active pane"},
			{"e", "open file in $EDITOR"},
			{"r", "reload current view"},
			{"?", "toggle this help"},
			{"q / ctrl+c", "quit"},
		}},
		{"File tree", []bind{
			{"j / k", "move (diff preview follows)"},
			{"h / l", "collapse / expand folder, h jumps to parent"},
			{"enter", "toggle folder / open file (focus diff)"},
			{"space", "toggle review for the whole file"},
			{"S / ctrl+space", "toggle skimmed for the whole file"},
			{"m", "mark menu for file or folder (incl. permanent marks)"},
			{"s", "stage / unstage file or folder"},
			{"g / G", "top / bottom"},
		}},
		{"Files view", []bind{
			{"4", "full repository tree; every file is one review unit"},
			{"space", "mark the current version reviewed, incl. all past changes"},
			{"S / ctrl+space", "same, but skimmed"},
		}},
		{"Commit list", []bind{
			{"j / k", "move (diff preview follows)"},
			{"enter", "open the commit's file tree (esc: back)"},
			{"space", "toggle review for the whole commit"},
			{"S / ctrl+space", "toggle skimmed for the whole commit"},
		}},
		{"Diff", []bind{
			{"j / k", "move through hunks (or lines)"},
			{"a", "toggle hunk ↔ line mode"},
			{"v", "visual multi-line select"},
			{"space", "toggle reviewed"},
			{"S / ctrl+space", "toggle skimmed (read over, makes sense)"},
			{"s", "stage / unstage hunk or selected lines"},
			{"esc", "leave visual (back to hunk mode) / back to tree"},
			{"g / G", "top / bottom"},
		}},
	}
	var b strings.Builder
	for i, sec := range sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(stTitle.Render(sec.title) + "\n")
		for _, bd := range sec.binds {
			b.WriteString(fmt.Sprintf("  %s %s\n",
				stStaged.Render(padRight(bd.k, 16)), bd.d))
		}
	}
	b.WriteString("\n" + stDim.Render("press any key to close"))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorderF).
		Padding(1, 3).
		Render(stTitle.Render("revu · keybindings") + "\n\n" + strings.TrimRight(b.String(), "\n"))
}

func (a *App) statusBar() string {
	if a.searching {
		return stTitle.Render(truncate(" /"+a.searchInput+"█", a.w))
	}
	if a.status != "" {
		return stStatusMsg.Render(truncate(" "+a.status, a.w))
	}
	var hints string
	switch {
	case a.focus == focusTree && a.view == viewCommits && a.commitOpen == nil:
		hints = "j/k move · enter files · space review commit · S skim · [/] view · + zoom · ? help · q quit"
	case a.focus == focusTree && a.view == viewFiles:
		hints = "j/k move · h/l fold · enter open · space review file · S skim · m mark · e edit · [/] view · + zoom · ? help · q quit"
	case a.focus == focusTree:
		hints = "j/k move · h/l fold · enter open · space review · S skim · m mark · s stage · e edit · [/] view · + zoom · ? help · q quit"
	default:
		hints = "j/k move · a hunk/line · v visual · space review · S skim · s stage · e edit · esc back · ? help · q quit"
	}
	viewName := "LOCAL"
	switch a.view {
	case viewCommits:
		viewName = "COMMITS"
	case viewPR:
		viewName = "PR"
	case viewFiles:
		viewName = "FILES"
	}
	search := ""
	if a.searchQuery != "" {
		search = " /" + a.searchQuery
	}
	rev := ""
	if a.hasReview() {
		rev = " ✎"
	}
	return stStatus.Render(truncate(fmt.Sprintf(" [%s ctx:%d%s%s] %s", viewName, a.context, search, rev, hints), a.w))
}
