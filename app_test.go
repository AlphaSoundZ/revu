package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "t@t.t")
	gitT(t, dir, "config", "user.name", "t")
	os.MkdirAll(filepath.Join(dir, "src/deep"), 0o755)
	os.WriteFile(filepath.Join(dir, "src/deep/b.txt"), []byte("line1\nline2\nline3\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "init")
	// staged change in src/deep/b.txt
	os.WriteFile(filepath.Join(dir, "src/deep/b.txt"), []byte("line1\nCHANGED\nline3\n"), 0o644)
	gitT(t, dir, "add", "src/deep/b.txt")
	// unstaged change in a.txt
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nunstaged\n"), 0o644)
	// untracked file
	os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new\n"), 0o644)
	// resolve symlinks (macOS /tmp -> /private/tmp) to match git rev-parse
	real, _ := filepath.EvalSymlinks(dir)
	return real
}

func key(a *App, k string) {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "ctrl+j":
		msg = tea.KeyMsg{Type: tea.KeyCtrlJ}
	case "ctrl+k":
		msg = tea.KeyMsg{Type: tea.KeyCtrlK}
	case "ctrl+d":
		msg = tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+u":
		msg = tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+o":
		msg = tea.KeyMsg{Type: tea.KeyCtrlO}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	a.Update(msg)
}

func newTestApp(t *testing.T, root string) *App {
	t.Helper()
	store, err := LoadStore(root)
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewApp(root, store)
	if err != nil {
		t.Fatal(err)
	}
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return a
}

func findEntry(files []*FileEntry, path string) *FileEntry {
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	return nil
}

func TestLocalLoad(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)

	if len(a.localFiles) != 3 {
		t.Fatalf("want 3 files, got %d", len(a.localFiles))
	}
	b := findEntry(a.localFiles, "src/deep/b.txt")
	if b == nil || !b.Staged {
		t.Fatal("src/deep/b.txt should be staged")
	}
	if aa := findEntry(a.localFiles, "a.txt"); aa == nil || aa.Staged {
		t.Fatal("a.txt should be unstaged")
	}
	if u := findEntry(a.localFiles, "untracked.txt"); u == nil || !u.Untracked {
		t.Fatal("untracked.txt should be untracked")
	}
	_, tot := b.Counts(a.store)
	if tot != 2 { // -line2 +CHANGED
		t.Fatalf("want 2 changed lines, got %d", tot)
	}
}

func TestHunkToggleAndPersistence(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)

	// tree rows: src/ deep/ b.txt a.txt untracked.txt
	key(a, "j")
	key(a, "j")
	n := a.localTree.Current()
	if n == nil || n.Path != "src/deep/b.txt" {
		t.Fatalf("cursor should be on src/deep/b.txt, got %+v", n)
	}
	key(a, "enter")
	if a.focus != focusDiff {
		t.Fatal("enter should focus diff pane")
	}
	key(a, " ") // toggle first hunk reviewed
	e := a.diff.entry
	rev, tot := e.Counts(a.store)
	if rev != tot || rev == 0 {
		t.Fatalf("hunk toggle: want all %d reviewed, got %d", tot, rev)
	}
	if _, err := os.Stat(filepath.Join(root, ".revu", "reviewed.json")); err != nil {
		t.Fatal("review state file should exist:", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".revu", ".gitignore")); err != nil {
		t.Fatal(".revu/.gitignore should exist:", err)
	}

	// untoggle
	key(a, " ")
	rev, _ = e.Counts(a.store)
	if rev != 0 {
		t.Fatalf("second toggle should clear, got %d reviewed", rev)
	}

	// esc returns to tree
	key(a, "esc")
	if a.focus != focusTree {
		t.Fatal("esc should return focus to tree")
	}
}

func TestLineModeAndVisual(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	key(a, "j")
	key(a, "j")
	key(a, "enter")

	key(a, "a") // line mode
	if !a.diff.lineMode {
		t.Fatal("a should enable line mode")
	}
	key(a, " ") // toggle single line
	rev, tot := a.diff.entry.Counts(a.store)
	if rev != 1 || tot != 2 {
		t.Fatalf("want 1/2 reviewed, got %d/%d", rev, tot)
	}
	// partial state -> file style should be partial (orange)
	if st := fileStyle(a.diff.entry, rev, tot); st.GetForeground() != colPartial {
		t.Fatal("partially reviewed file should be orange")
	}

	// visual select both lines, toggle -> all reviewed
	key(a, "g")
	key(a, "v")
	if !a.diff.visual {
		t.Fatal("v should enter visual mode")
	}
	key(a, "j")
	key(a, " ")
	rev, tot = a.diff.entry.Counts(a.store)
	if rev != tot {
		t.Fatalf("visual toggle: want all reviewed, got %d/%d", rev, tot)
	}
	if a.diff.visual {
		t.Fatal("space should exit visual mode")
	}
	if st := fileStyle(a.diff.entry, rev, tot); st.GetForeground() != colReviewed {
		t.Fatal("fully reviewed file should be blue")
	}
}

func TestUnstagedNotTogglable(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	// move to a.txt (unstaged): rows src/ deep/ b.txt a.txt untracked.txt
	for i := 0; i < 3; i++ {
		key(a, "j")
	}
	n := a.localTree.Current()
	if n == nil || n.Path != "a.txt" {
		t.Fatalf("cursor should be on a.txt, got %+v", n)
	}
	key(a, "enter")
	key(a, " ")
	if a.status == "" {
		t.Fatal("toggling unstaged hunk should set a status message")
	}
	rev, _ := findEntry(a.localFiles, "a.txt").Counts(a.store)
	if rev != 0 {
		t.Fatal("unstaged lines must not be reviewable")
	}
}

func TestReviewSurvivesCommit(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	key(a, "j")
	key(a, "j")
	key(a, "enter")
	key(a, " ") // review staged hunk

	// commit, then re-diff the same change as if it were a PR diff:
	// content-addressed IDs must match, so the mark survives.
	out, err := runCmd(root, "git", "diff", "--cached", "--no-color")
	if err != nil {
		t.Fatal(err)
	}
	files := parseUnifiedDiff(out)
	if len(files) != 1 {
		t.Fatalf("want 1 file in diff, got %d", len(files))
	}
	for _, h := range files[0].Hunks {
		h.Reviewable = true
	}
	files[0].Staged = true
	assignIDs(files[0])
	rev, tot := files[0].Counts(a.store)
	if rev != tot || tot == 0 {
		t.Fatalf("review mark should survive re-parse: got %d/%d", rev, tot)
	}
}

func TestTreeSpaceTogglesWholeFile(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	key(a, "j")
	key(a, "j") // src/deep/b.txt
	key(a, " ")
	if a.focus != focusTree {
		t.Fatal("space in tree must not change focus")
	}
	e := findEntry(a.localFiles, "src/deep/b.txt")
	rev, tot := e.Counts(a.store)
	if rev != tot || tot == 0 {
		t.Fatalf("tree space: want all %d reviewed, got %d", tot, rev)
	}
	key(a, " ")
	if rev, _ := e.Counts(a.store); rev != 0 {
		t.Fatalf("second tree space should clear, got %d reviewed", rev)
	}
	// unstaged file must refuse with a message
	key(a, "j") // a.txt
	key(a, " ")
	if a.status == "" {
		t.Fatal("toggling an unstaged file should set a status message")
	}
	if rev, _ := findEntry(a.localFiles, "a.txt").Counts(a.store); rev != 0 {
		t.Fatal("unstaged file must not be reviewable")
	}
}

func TestEscLeavesVisualIntoHunkMode(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	key(a, "j")
	key(a, "j")
	key(a, "enter")
	key(a, "v") // visual implies line mode
	if !a.diff.visual || !a.diff.lineMode {
		t.Fatal("v should enter visual line mode")
	}
	key(a, "esc")
	if a.diff.visual {
		t.Fatal("esc should leave visual mode")
	}
	if a.diff.lineMode {
		t.Fatal("esc from visual should drop back to hunk mode")
	}
	if a.focus != focusDiff {
		t.Fatal("first esc should stay in the diff pane")
	}
	key(a, "esc")
	if a.focus != focusTree {
		t.Fatal("second esc should return to the tree")
	}
}

func TestHelpPopup(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	key(a, "?")
	if !a.showHelp {
		t.Fatal("? should open help")
	}
	if v := a.View(); !strings.Contains(v, "keybindings") {
		t.Fatal("help view should render keybindings")
	}
	key(a, "j")
	if a.showHelp {
		t.Fatal("any key should close help")
	}
	if a.localTree.cursor != 0 {
		t.Fatal("key closing help must not leak into the tree")
	}
}

func setupBigRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "t@t.t")
	gitT(t, dir, "config", "user.name", "t")
	var orig, changed strings.Builder
	for i := 0; i < 200; i++ {
		orig.WriteString(fmt.Sprintf("line%d\n", i))
		if i%10 == 0 {
			changed.WriteString(fmt.Sprintf("CHANGED%d\n", i))
		} else {
			changed.WriteString(fmt.Sprintf("line%d\n", i))
		}
	}
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(orig.String()), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "init")
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(changed.String()), 0o644)
	gitT(t, dir, "add", "big.txt")
	real, _ := filepath.EvalSymlinks(dir)
	return real
}

func TestCtrlScrollFromTree(t *testing.T) {
	real := setupBigRepo(t)
	a := newTestApp(t, real)
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	a.View() // sets the diff pane height
	if a.focus != focusTree {
		t.Fatal("should start in tree focus")
	}
	key(a, "J")
	if a.diff.scroll != 1 {
		t.Fatalf("J from tree should scroll diff, scroll=%d", a.diff.scroll)
	}
	key(a, "J")
	key(a, "K")
	if a.diff.scroll != 1 {
		t.Fatalf("K should scroll back, scroll=%d", a.diff.scroll)
	}
	a.View()
	if a.diff.scroll != 1 {
		t.Fatal("free scroll must survive a render without snapping to cursor")
	}
	// ctrl+j stays as a silent alias
	key(a, "enter")
	key(a, "ctrl+j")
	if a.diff.scroll != 2 {
		t.Fatalf("ctrl+j alias should scroll, scroll=%d", a.diff.scroll)
	}
	key(a, "j")
	a.View()
	if a.diff.free {
		t.Fatal("cursor movement should end free scrolling")
	}

	// ctrl+d/u half-page scroll works from any focus
	key(a, "esc") // back to tree
	before := a.diff.scroll
	key(a, "ctrl+d")
	if a.diff.scroll <= before {
		t.Fatal("ctrl+d from tree should scroll the diff half a page")
	}
	key(a, "ctrl+u")
	if a.diff.scroll != before {
		t.Fatalf("ctrl+u should scroll back, got %d want %d", a.diff.scroll, before)
	}
}

func TestRemovedLinesRed(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	key(a, "j")
	key(a, "j") // src/deep/b.txt: -line2 +CHANGED
	d := a.diff
	var minusRow, plusRow dRow
	for _, r := range d.rows {
		if r.kind != rowLine {
			continue
		}
		switch d.entry.Hunks[r.hunk].Lines[r.line].Origin {
		case '-':
			minusRow = r
		case '+':
			plusRow = r
		}
	}
	if d.rowStyle(minusRow).GetForeground() != colRemoved {
		t.Fatal("removed line should be red")
	}
	if d.rowStyle(plusRow).GetForeground() != colStaged {
		t.Fatal("staged added line should stay green")
	}
	// review the hunk -> reviewed wins over red
	key(a, "enter")
	key(a, " ")
	if d.rowStyle(minusRow).GetForeground() != colReviewed {
		t.Fatal("reviewed removed line should be blue")
	}
}

func TestContextResize(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "t@t.t")
	gitT(t, dir, "config", "user.name", "t")
	var orig, changed strings.Builder
	for i := 0; i < 30; i++ {
		orig.WriteString(fmt.Sprintf("line%d\n", i))
		// two changes 3 unchanged lines apart: merged at -U3, split at -U1
		if i == 10 || i == 14 {
			changed.WriteString(fmt.Sprintf("CHANGED%d\n", i))
		} else {
			changed.WriteString(fmt.Sprintf("line%d\n", i))
		}
	}
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte(orig.String()), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "init")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte(changed.String()), 0o644)
	gitT(t, dir, "add", "f.txt")
	real, _ := filepath.EvalSymlinks(dir)

	f1, err := loadLocal(real, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f1[0].Hunks); n != 2 {
		t.Fatalf("-U1 should split into 2 hunks, got %d", n)
	}
	f3, err := loadLocal(real, 3)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f3[0].Hunks); n != 1 {
		t.Fatalf("-U3 should merge into 1 hunk, got %d", n)
	}
	// review marks survive a context change: IDs ignore context lines
	store, _ := LoadStore(real)
	for _, h := range f1[0].Hunks {
		for _, l := range h.Lines {
			if l.Origin != ' ' {
				store.Set(l.ID, true)
			}
		}
	}
	if rev, tot := f3[0].Counts(store); rev != tot || tot == 0 {
		t.Fatalf("marks must survive context change, got %d/%d", rev, tot)
	}

	a := newTestApp(t, real)
	if a.context != defaultContext {
		t.Fatalf("default context should be %d, got %d", defaultContext, a.context)
	}
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("}")})
	if a.context != 2 {
		t.Fatalf("} should grow context to 2, got %d", a.context)
	}
	if cmd == nil {
		t.Fatal("} should trigger a reload")
	}
	// { clamps at 0
	for i := 0; i < 5; i++ {
		a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("{")})
	}
	if a.context != 0 {
		t.Fatalf("{ should clamp at 0, got %d", a.context)
	}
}

func TestViewCyclingAndTabs(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	if !strings.Contains(a.View(), "[LOCAL]") {
		t.Fatal("active LOCAL view should be bracketed in the tabs")
	}
	if !strings.Contains(a.View(), "COMMITS") {
		t.Fatal("inactive views should still be visible in the tabs")
	}
	key(a, "]")
	if a.view != viewCommits {
		t.Fatalf("] from LOCAL should go to COMMITS, got %v", a.view)
	}
	if !strings.Contains(a.View(), "[COMMITS]") {
		t.Fatal("active COMMITS view should be bracketed")
	}
	key(a, "]")
	if a.view != viewPR {
		t.Fatalf("] from COMMITS should go to PR, got %v", a.view)
	}
	key(a, "]")
	if a.view != viewLocal {
		t.Fatal("] should wrap around to LOCAL")
	}
	key(a, "[")
	if a.view != viewPR {
		t.Fatal("[ from LOCAL should wrap to PR")
	}
	key(a, "1")
	if a.view != viewLocal {
		t.Fatal("1 should jump to LOCAL")
	}
	// tab no longer switches views
	key(a, "tab")
	if a.view != viewLocal {
		t.Fatal("tab must not switch views anymore")
	}
}

func TestCommitsView(t *testing.T) {
	root := setupRepo(t)
	// second commit so the list has two entries (no upstream -> fallback log)
	os.WriteFile(filepath.Join(root, "c.txt"), []byte("one\ntwo\n"), 0o644)
	gitT(t, root, "add", "c.txt")
	gitT(t, root, "commit", "-qm", "add c.txt")

	a := newTestApp(t, root)
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	if a.view != viewCommits || !a.commitsLoading {
		t.Fatal("2 should switch to COMMITS and start loading")
	}
	if cmd == nil {
		t.Fatal("switching to COMMITS should return a load cmd")
	}
	a.Update(cmd())
	if a.commitsErr != "" {
		t.Fatal("commits load failed:", a.commitsErr)
	}
	if len(a.commits) != 2 {
		t.Fatalf("want 2 commits, got %d", len(a.commits))
	}
	c := a.commitList.Current()
	if c.Subject != "add c.txt" {
		t.Fatalf("newest commit first, got %q", c.Subject)
	}
	// diff pane shows the commit's files, hunk headers carry the path
	if a.diff.entry == nil || len(a.diff.entry.Hunks) == 0 {
		t.Fatal("commit diff should be shown in the right pane")
	}
	if !strings.Contains(a.diff.entry.Hunks[0].Header, "c.txt") {
		t.Fatal("hunk header should carry the file path")
	}

	// space marks the whole commit as reviewed; the commit contains c.txt
	// plus the b.txt change that was already staged in setupRepo (4 lines)
	key(a, " ")
	rev, tot := c.Counts(a.store)
	if rev != tot || tot != 4 {
		t.Fatalf("commit toggle: want 4/4 reviewed, got %d/%d", rev, tot)
	}
	key(a, " ")
	if rev, _ := c.Counts(a.store); rev != 0 {
		t.Fatal("second toggle should clear the commit")
	}

	// enter opens the commit's file tree
	key(a, "enter")
	if a.commitOpen == nil || a.commitTree == nil {
		t.Fatal("enter should open the commit's file tree")
	}
	if a.focus != focusTree {
		t.Fatal("opening the tree should keep tree focus")
	}
	// rows: src/ deep/ b.txt c.txt
	key(a, "G")
	n := a.commitTree.Current()
	if n == nil || n.Path != "c.txt" {
		t.Fatalf("cursor should be on c.txt, got %+v", n)
	}
	// enter focuses the single-file diff of the commit
	key(a, "enter")
	if a.focus != focusDiff {
		t.Fatal("enter on a file should focus the diff")
	}
	if p := a.diff.CurrentFilePath(); p != "c.txt" {
		t.Fatalf("editor target should be c.txt, got %q", p)
	}
	// hunk toggling reviews only this file's lines
	key(a, " ")
	if rev, _ := c.Counts(a.store); rev != 2 {
		t.Fatalf("hunk toggle should review c.txt's 2 lines, got %d", rev)
	}
	// esc chain: diff -> tree -> commit list
	key(a, "esc")
	if a.focus != focusTree || a.commitOpen == nil {
		t.Fatal("first esc should return to the commit file tree")
	}
	key(a, "esc")
	if a.commitOpen != nil {
		t.Fatal("second esc should close the tree and return to the commit list")
	}
	if a.diff.entry == nil || len(a.diff.entry.Hunks) != 2 {
		t.Fatal("commit list should show the combined diff again")
	}
}

func TestCopyReviewPrompt(t *testing.T) {
	var captured string
	old := copyToClipboard
	copyToClipboard = func(s string) error { captured = s; return nil }
	defer func() { copyToClipboard = old }()

	root := setupRepo(t)
	a := newTestApp(t, root)
	key(a, "j")
	key(a, "j")      // src/deep/b.txt
	key(a, "ctrl+o") // from the tree: no line range
	want := "review diese änderungen:\nDatei: src/deep/b.txt\n"
	if captured != want {
		t.Fatalf("tree prompt mismatch:\n got %q\nwant %q", captured, want)
	}
	if !strings.Contains(a.status, "copied") {
		t.Fatal("copy should report a status message")
	}
	// from the diff: includes the hunk's changed-line range
	key(a, "enter")
	key(a, "ctrl+o")
	want = "review diese änderungen:\nDatei: src/deep/b.txt\nZeilen: 2\n"
	if captured != want {
		t.Fatalf("diff prompt mismatch:\n got %q\nwant %q", captured, want)
	}
}

func TestCommitReviewMarksCarryToOtherDiffs(t *testing.T) {
	root := setupRepo(t)
	// commit the staged change, then review the commit
	gitT(t, root, "commit", "-qm", "change b")
	a := newTestApp(t, root)
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	a.Update(cmd())
	key(a, " ") // review newest commit ("change b")

	// the same change re-parsed from a branch diff (as the PR view would)
	out, err := runCmd(root, "git", "diff", "-U1", "--no-color", "HEAD~1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	files := parseUnifiedDiff(out)
	for _, f := range files {
		for _, h := range f.Hunks {
			h.Reviewable = true
		}
		assignIDs(f)
	}
	rev, tot := files[0].Counts(a.store)
	if rev != tot || tot == 0 {
		t.Fatalf("commit review should carry over to the PR diff, got %d/%d", rev, tot)
	}
}

func TestUnstagedSoftColors(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "t@t.t")
	gitT(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\n"), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "init")
	// unstaged: one removed, one added line
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\nNEW\n"), 0o644)
	real, _ := filepath.EvalSymlinks(dir)

	a := newTestApp(t, real)
	d := a.diff
	for _, r := range d.rows {
		if r.kind != rowLine {
			continue
		}
		switch d.entry.Hunks[r.hunk].Lines[r.line].Origin {
		case '-':
			if d.rowStyle(r).GetForeground() != colRemSoft {
				t.Fatal("unstaged removed line should be soft red")
			}
		case '+':
			if d.rowStyle(r).GetForeground() != colAddSoft {
				t.Fatal("unstaged added line should be soft green")
			}
		}
	}
}

func TestStageFileFromTree(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	// move to untracked.txt (last row)
	key(a, "G")
	n := a.localTree.Current()
	if n == nil || n.Path != "untracked.txt" {
		t.Fatalf("cursor should be on untracked.txt, got %+v", n)
	}
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd == nil {
		t.Fatal("s should trigger a reload")
	}
	a.Update(cmd())
	e := findEntry(a.localFiles, "untracked.txt")
	if e == nil || !e.Staged || e.Untracked {
		t.Fatalf("untracked.txt should be staged after s, got %+v", e)
	}
}

func TestStageHunkFromDiff(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	// a.txt has an unstaged hunk
	for i := 0; i < 3; i++ {
		key(a, "j")
	}
	key(a, "enter")
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd == nil {
		t.Fatalf("s on unstaged hunk should stage and reload, status=%q", a.status)
	}
	a.Update(cmd())
	e := findEntry(a.localFiles, "a.txt")
	if e == nil || !e.Staged {
		t.Fatal("a.txt should be staged after staging its hunk")
	}
	// now reviewable
	rev, tot := e.Counts(a.store)
	if tot == 0 || rev != 0 {
		t.Fatalf("staged hunk should be reviewable and unreviewed, got %d/%d", rev, tot)
	}
	// s again toggles: the staged hunk is unstaged
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd == nil {
		t.Fatalf("s on a staged hunk should unstage, status=%q", a.status)
	}
	a.Update(cmd())
	e = findEntry(a.localFiles, "a.txt")
	if e == nil || e.Staged {
		t.Fatal("a.txt should be unstaged again after the second s")
	}
}

func TestUnstageFileFromTree(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	key(a, "j")
	key(a, "j") // src/deep/b.txt, staged
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd == nil {
		t.Fatalf("s on a staged file should unstage, status=%q", a.status)
	}
	a.Update(cmd())
	e := findEntry(a.localFiles, "src/deep/b.txt")
	if e == nil || e.Staged {
		t.Fatal("b.txt should be unstaged after s")
	}
	// and back
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	a.Update(cmd())
	e = findEntry(a.localFiles, "src/deep/b.txt")
	if e == nil || !e.Staged {
		t.Fatal("b.txt should be staged again after the second s")
	}
}

func TestUnstageSingleLine(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "t@t.t")
	gitT(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\n"), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "init")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\nADD1\nADD2\ntwo\n"), 0o644)
	gitT(t, dir, "add", "f.txt") // both lines staged
	real, _ := filepath.EvalSymlinks(dir)

	a := newTestApp(t, real)
	key(a, "enter")
	key(a, "a") // line mode, cursor on ADD1
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd == nil {
		t.Fatalf("s on a staged line should unstage it, status=%q", a.status)
	}
	a.Update(cmd())
	stagedOut, err := runCmd(real, "git", "diff", "--cached", "--no-color")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stagedOut, "+ADD1") || !strings.Contains(stagedOut, "+ADD2") {
		t.Fatalf("only ADD2 should remain staged:\n%s", stagedOut)
	}
}

func TestStageSingleLine(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "t@t.t")
	gitT(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\n"), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "init")
	// one hunk with two unstaged added lines
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\nADD1\nADD2\ntwo\n"), 0o644)
	real, _ := filepath.EvalSymlinks(dir)

	a := newTestApp(t, real)
	key(a, "enter")
	key(a, "a") // line mode, cursor on ADD1
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd == nil {
		t.Fatalf("s on a single line should stage it, status=%q", a.status)
	}
	a.Update(cmd())
	// index must now contain ADD1 but not ADD2
	stagedOut, err := runCmd(real, "git", "diff", "--cached", "--no-color")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stagedOut, "+ADD1") || strings.Contains(stagedOut, "+ADD2") {
		t.Fatalf("only ADD1 should be staged:\n%s", stagedOut)
	}
	e := findEntry(a.localFiles, "f.txt")
	_, tot := e.Counts(a.store)
	if tot != 1 {
		t.Fatalf("one staged reviewable line expected, got %d", tot)
	}
}

func TestSearchInTree(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	key(a, "enter") // collapse src/
	if len(a.localTree.rows) != 3 {
		t.Fatalf("src should be collapsed, rows=%d", len(a.localTree.rows))
	}
	key(a, "/")
	if !a.searching {
		t.Fatal("/ should open the search bar")
	}
	key(a, "b.txt")
	key(a, "enter")
	if a.searching {
		t.Fatal("enter should close the search bar")
	}
	if a.searchQuery != "b.txt" {
		t.Fatalf("query should stay active after enter, got %q", a.searchQuery)
	}
	n := a.localTree.Current()
	if n == nil || n.Path != "src/deep/b.txt" {
		t.Fatalf("search should reveal and select src/deep/b.txt, got %+v", n)
	}
	// n cycles through matches (a.txt, b.txt, untracked.txt all contain "txt")
	key(a, "esc")
	if a.searchQuery != "" {
		t.Fatal("esc should cancel the search and clear highlights")
	}
	key(a, "/")
	key(a, "txt")
	key(a, "enter")
	first := a.localTree.Current().Path
	key(a, "n")
	second := a.localTree.Current().Path
	if first == second {
		t.Fatal("n should move to the next match")
	}
	key(a, "N")
	if a.localTree.Current().Path != first {
		t.Fatal("N should move back to the previous match")
	}
}

func TestSearchInDiff(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	key(a, "j")
	key(a, "j")
	key(a, "enter")
	key(a, "/")
	key(a, "CHANGED")
	key(a, "enter")
	d := a.diff
	if d.searchRow < 0 || !strings.Contains(d.rows[d.searchRow].text, "CHANGED") {
		t.Fatalf("search should locate CHANGED, searchRow=%d", d.searchRow)
	}
	key(a, "n") // wraps onto the only match
	if d.searchRow < 0 || !strings.Contains(d.rows[d.searchRow].text, "CHANGED") {
		t.Fatal("n should stay on the only match")
	}
	key(a, "esc")
	if a.searchQuery != "" || d.searchRow != -1 {
		t.Fatal("esc should clear the diff search state")
	}
}

func TestCommitsRangeWithRemote(t *testing.T) {
	root := setupRepo(t)
	bare := t.TempDir()
	gitT(t, bare, "init", "-q", "--bare")
	gitT(t, root, "remote", "add", "origin", bare)
	gitT(t, root, "push", "-qu", "origin", "HEAD")
	// one pushed and one unpushed commit on top of init
	os.WriteFile(filepath.Join(root, "p.txt"), []byte("p\n"), 0o644)
	gitT(t, root, "add", "p.txt")
	gitT(t, root, "commit", "-qm", "pushed-a")
	gitT(t, root, "push", "-q")
	os.WriteFile(filepath.Join(root, "n.txt"), []byte("n\n"), 0o644)
	gitT(t, root, "add", "n.txt")
	gitT(t, root, "commit", "-qm", "unpushed-b")

	commits, err := loadCommits(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	// no PR (local remote) -> upstream range: only the unpushed commit
	if len(commits) != 1 || commits[0].Subject != "unpushed-b" {
		t.Fatalf("want only the unpushed commit, got %d: %+v", len(commits), commits)
	}

	// the PR range (origin/<base>..HEAD) lists pushed AND unpushed
	// commits: base branch at the init commit, HEAD two commits ahead
	initHash, _ := runCmd(root, "git", "rev-list", "--max-parents=0", "HEAD")
	gitT(t, root, "branch", "base", strings.TrimSpace(initHash))
	gitT(t, root, "push", "-q", "origin", "base")
	out, err := runCmd(root, "git", "log", "--no-merges", "--format=%s", "origin/base..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	subjects := strings.Fields(strings.TrimSpace(out))
	if len(subjects) != 2 {
		t.Fatalf("base-range should include pushed and unpushed commits, got %v", subjects)
	}
}

func TestSearchInCommits(t *testing.T) {
	root := setupRepo(t)
	gitT(t, root, "commit", "-qm", "second commit")
	a := newTestApp(t, root)
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	a.Update(cmd())
	if a.commitList.Current().Subject != "second commit" {
		t.Fatal("newest commit should be selected")
	}
	key(a, "/")
	key(a, "init")
	key(a, "enter")
	if a.commitList.Current().Subject != "init" {
		t.Fatalf("search should select the init commit, got %q", a.commitList.Current().Subject)
	}
}

func TestTopBottomJump(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	key(a, ">")
	if a.localTree.cursor != len(a.localTree.rows)-1 {
		t.Fatal("> should jump to the bottom of the tree")
	}
	key(a, "<")
	if a.localTree.cursor != 0 {
		t.Fatal("< should jump to the top of the tree")
	}
	key(a, "j")
	key(a, "j")
	key(a, "enter")
	key(a, "a") // line mode: two selectable lines
	key(a, ">")
	if a.diff.cursor != len(a.diff.sels)-1 {
		t.Fatal("> should jump to the last line in the diff")
	}
	key(a, "<")
	if a.diff.cursor != 0 {
		t.Fatal("< should jump to the first line in the diff")
	}
}

func TestFileStatusLetters(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "t@t.t")
	gitT(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("a\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("b\n"), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "init")
	os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("a2\n"), 0o644)
	gitT(t, dir, "add", "keep.txt")
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("n\n"), 0o644)
	gitT(t, dir, "add", "new.txt")
	gitT(t, dir, "rm", "-q", "gone.txt")
	os.WriteFile(filepath.Join(dir, "untr.txt"), []byte("u\n"), 0o644)
	real, _ := filepath.EvalSymlinks(dir)

	files, err := loadLocal(real, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]byte{"keep.txt": 'M', "new.txt": 'A', "gone.txt": 'D', "untr.txt": 'A'}
	for path, status := range want {
		e := findEntry(files, path)
		if e == nil {
			t.Fatalf("%s missing", path)
		}
		if e.Status != status {
			t.Fatalf("%s: want status %c, got %c", path, status, e.Status)
		}
	}
	store, _ := LoadStore(real)
	out := NewTree(files, nil).View(40, 20, store, false, "")
	for _, s := range []string{" M keep.txt", " A new.txt", " D gone.txt", " A untr.txt"} {
		if !strings.Contains(out, s) {
			t.Fatalf("tree should render %q:\n%s", s, out)
		}
	}

	// staged + unstaged changes at once -> extra M prefix
	os.WriteFile(filepath.Join(real, "keep.txt"), []byte("a2\nworktree\n"), 0o644)
	files, err = loadLocal(real, 1)
	if err != nil {
		t.Fatal(err)
	}
	mixed := findEntry(files, "keep.txt")
	if got := mixed.StatusMarker(); got != "MM" {
		t.Fatalf("mixed file should render MM, got %q", got)
	}
	out = NewTree(files, nil).View(40, 20, store, false, "")
	if !strings.Contains(out, "MM keep.txt") {
		t.Fatalf("tree should render MM for mixed file:\n%s", out)
	}
}

func TestFolderColors(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	srcNode := a.localTree.rows[0].node
	if !srcNode.IsDir || srcNode.Path != "src" {
		t.Fatalf("first row should be src/, got %+v", srcNode)
	}
	// nothing reviewed -> white
	rev, tot := nodeCounts(srcNode, a.store)
	if tot != 2 || rev != 0 {
		t.Fatalf("src/ should aggregate 0/2, got %d/%d", rev, tot)
	}
	if dirStyle(rev, tot).GetForeground() != colUnstaged {
		t.Fatal("unreviewed folder should be white")
	}
	// one of two lines reviewed -> orange
	key(a, "j")
	key(a, "j")
	key(a, "enter")
	key(a, "a") // line mode
	key(a, " ")
	rev, tot = nodeCounts(srcNode, a.store)
	if rev != 1 {
		t.Fatalf("want 1 reviewed line, got %d", rev)
	}
	if dirStyle(rev, tot).GetForeground() != colPartial {
		t.Fatal("partially reviewed folder should be orange")
	}
	// all reviewed -> blue
	key(a, "j")
	key(a, " ")
	rev, tot = nodeCounts(srcNode, a.store)
	if rev != tot {
		t.Fatalf("want all reviewed, got %d/%d", rev, tot)
	}
	if dirStyle(rev, tot).GetForeground() != colReviewed {
		t.Fatal("fully reviewed folder should be blue")
	}
}

func TestScrollbar(t *testing.T) {
	real := setupBigRepo(t)
	a := newTestApp(t, real)
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if v := a.View(); !strings.Contains(v, "█") || !strings.Contains(v, "│") {
		t.Fatal("long diff should render a scrollbar with thumb and track")
	}

	// short diff: no scrollbar
	root := setupRepo(t)
	b := newTestApp(t, root)
	key(b, "j")
	key(b, "j") // b.txt, few rows
	if v := b.View(); strings.Contains(v, "█") {
		t.Fatal("short diff should not render a scrollbar thumb")
	}
}

func TestMouseWheel(t *testing.T) {
	real := setupBigRepo(t)
	a := newTestApp(t, real)
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	a.View() // sets diff pane height
	wheel := func(x int, btn tea.MouseButton) {
		a.Update(tea.MouseMsg{X: x, Y: 5, Button: btn, Action: tea.MouseActionPress})
	}
	wheel(80, tea.MouseButtonWheelDown) // over the diff pane
	if a.diff.scroll != 3 {
		t.Fatalf("wheel over diff should scroll 3 rows, got %d", a.diff.scroll)
	}
	wheel(80, tea.MouseButtonWheelUp)
	if a.diff.scroll != 0 {
		t.Fatalf("wheel up should scroll back, got %d", a.diff.scroll)
	}
	if a.focus != focusTree {
		t.Fatal("wheel must not steal focus")
	}
	// over the tree pane: moves the selection
	root := setupRepo(t)
	b := newTestApp(t, root)
	b.View()
	b.Update(tea.MouseMsg{X: 2, Y: 5, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if b.localTree.cursor != 1 {
		t.Fatalf("wheel over tree should move the cursor, got %d", b.localTree.cursor)
	}
}

func TestReviewProgressIndicator(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	if !strings.Contains(a.View(), "0%") {
		t.Fatal("LOCAL view should show 0% before any review")
	}
	key(a, "j")
	key(a, "j")
	key(a, "enter")
	key(a, "a") // line mode
	key(a, " ") // 1 of 2 staged lines reviewed
	if !strings.Contains(a.View(), "50%") {
		t.Fatal("LOCAL view should show 50% after half the staged lines")
	}
	key(a, "j")
	key(a, " ")
	if !strings.Contains(a.View(), "100%") {
		t.Fatal("LOCAL view should show 100% when everything is reviewed")
	}
	// commits tab computes its own progress
	key(a, "esc")
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	a.Update(cmd())
	rev, tot := a.reviewProgress()
	if tot == 0 {
		t.Fatal("COMMITS view should aggregate commit lines")
	}
	if want := rev * 100 / tot; !strings.Contains(a.View(), fmt.Sprintf("%d%%", want)) {
		t.Fatalf("COMMITS view should show %d%%", want)
	}
}

func TestExcludedFiles(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "t@t.t")
	gitT(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "code.go"), []byte("a\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "snapshot.json"), []byte("{}\n"), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "init")
	os.WriteFile(filepath.Join(dir, "code.go"), []byte("a2\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "snapshot.json"), []byte("{\"x\":1}\n"), 0o644)
	gitT(t, dir, "add", ".")
	real, _ := filepath.EvalSymlinks(dir)

	a := newTestApp(t, real)
	snap := findEntry(a.localFiles, "snapshot.json")
	if snap == nil || !snap.Excluded {
		t.Fatal("snapshot.json should be excluded by default")
	}
	if _, tot := snap.Counts(a.store); tot != 0 {
		t.Fatal("excluded file must count as 0/0")
	}
	// progress only counts code.go (2 lines: -a +a2)
	if _, tot := a.reviewProgress(); tot != 2 {
		t.Fatalf("progress should ignore snapshot.json, tot=%d", tot)
	}
	// not toggleable, from tree nor diff
	key(a, "j") // snapshot.json (sorted after code.go)
	n := a.localTree.Current()
	if n == nil || n.Path != "snapshot.json" {
		t.Fatalf("cursor should be on snapshot.json, got %+v", n)
	}
	key(a, " ")
	if !strings.Contains(a.status, "excluded") {
		t.Fatalf("toggling an excluded file should explain itself, status=%q", a.status)
	}
	key(a, "enter")
	key(a, " ")
	if !strings.Contains(a.status, "excluded") {
		t.Fatal("diff toggle on an excluded file should refuse too")
	}

	// custom config overrides the default
	os.MkdirAll(filepath.Join(real, ".revu"), 0o755)
	os.WriteFile(filepath.Join(real, ".revu", "config.json"),
		[]byte(`{"exclude":["*.go"]}`), 0o644)
	files, _ := loadLocal(real, 1)
	b := newTestApp(t, real)
	b.applyExcludes(files)
	if e := findEntry(files, "snapshot.json"); e.Excluded {
		t.Fatal("custom exclude list should replace the default")
	}
	if e := findEntry(files, "code.go"); !e.Excluded {
		t.Fatal("*.go should now be excluded")
	}
}

func TestRunCheck(t *testing.T) {
	root := setupRepo(t)
	store, _ := LoadStore(root)

	// staged but unreviewed -> fail with a file listing
	msg, ok := runCheck(root, store)
	if ok {
		t.Fatal("check must fail while staged lines are unreviewed")
	}
	if !strings.Contains(msg, "0/2") || !strings.Contains(msg, "src/deep/b.txt") {
		t.Fatalf("report should name the unreviewed file:\n%s", msg)
	}

	// review everything -> pass
	a := newTestApp(t, root)
	key(a, "j")
	key(a, "j")
	key(a, " ")
	msg, ok = runCheck(root, a.store)
	if !ok {
		t.Fatalf("check should pass at 100%%:\n%s", msg)
	}

	// nothing staged -> pass
	gitT(t, root, "reset", "-q")
	msg, ok = runCheck(root, a.store)
	if !ok || !strings.Contains(msg, "nothing staged") {
		t.Fatalf("check should pass with nothing staged:\n%s", msg)
	}
}

func TestRunCheckIgnoresExcluded(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "t@t.t")
	gitT(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "snapshot.json"), []byte("{}\n"), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "init")
	os.WriteFile(filepath.Join(dir, "snapshot.json"), []byte("{\"x\":1}\n"), 0o644)
	gitT(t, dir, "add", ".")
	real, _ := filepath.EvalSymlinks(dir)

	store, _ := LoadStore(real)
	msg, ok := runCheck(real, store)
	if !ok {
		t.Fatalf("only excluded files staged -> check must pass:\n%s", msg)
	}
}

func TestFoldToggle(t *testing.T) {
	root := setupRepo(t)
	a := newTestApp(t, root)
	n := a.localTree.Current()
	if n == nil || !n.IsDir || n.Path != "src" {
		t.Fatalf("first row should be src/, got %+v", n)
	}
	rowsBefore := len(a.localTree.rows)
	key(a, "enter") // collapse src/
	if len(a.localTree.rows) >= rowsBefore {
		t.Fatal("collapsing src/ should hide children")
	}
	key(a, "l") // expand again
	if len(a.localTree.rows) != rowsBefore {
		t.Fatal("expanding src/ should restore children")
	}
}
