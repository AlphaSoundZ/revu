package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type Commit struct {
	Hash    string
	Subject string
	Files   []*FileEntry // parsed commit diff, all hunks reviewable
}

func (c *Commit) Short() string {
	if len(c.Hash) > 7 {
		return c.Hash[:7]
	}
	return c.Hash
}

func (c *Commit) Counts(st *Store) (int, int) {
	rev, tot := 0, 0
	for _, f := range c.Files {
		r, t := f.Counts(st)
		rev += r
		tot += t
	}
	return rev, tot
}

// ToggleReviewed flips review state for every changed line of the
// commit; skim toggles the skimmed mark instead.
func (c *Commit) ToggleReviewed(st *Store, skim bool) string {
	var ids []string
	all := true
	for _, f := range c.Files {
		if f.Excluded {
			continue
		}
		if f.Binary && f.BinaryID != "" {
			ids = append(ids, f.BinaryID)
			if !st.In(f.BinaryID, skim) {
				all = false
			}
		}
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if l.Origin == '+' || l.Origin == '-' {
					ids = append(ids, l.ID)
					if !st.In(l.ID, skim) {
						all = false
					}
				}
			}
		}
	}
	if len(ids) == 0 {
		return "commit has no reviewable lines"
	}
	for _, id := range ids {
		st.Mark(id, skim, !all)
	}
	if err := st.Save(); err != nil {
		return "failed to save review state: " + err.Error()
	}
	return ""
}

// AnySkimmed reports whether any counted line of the commit is skimmed.
func (c *Commit) AnySkimmed(st *Store) bool {
	for _, f := range c.Files {
		if f.AnySkimmed(st) {
			return true
		}
	}
	return false
}

// Entry flattens all files of the commit into one entry for the diff pane.
// Hunk headers carry the file path; hunks keep their original identity so
// toggling in the diff pane hits the same line IDs.
func (c *Commit) Entry() *FileEntry {
	e := &FileEntry{Path: c.Short() + " " + c.Subject, Staged: true}
	for _, f := range c.Files {
		e.Hunks = append(e.Hunks, f.Hunks...)
	}
	return e
}

// commitRange picks which commits to list: the PR's commits
// (origin/<base>..HEAD) when an open PR exists and the base ref is
// available locally, otherwise unpushed commits (@{upstream}..HEAD).
func commitRange(root string) string {
	if out, err := runCmd(root, "gh", "pr", "view", "--json", "baseRefName", "--jq", ".baseRefName"); err == nil {
		if base := strings.TrimSpace(out); base != "" {
			ref := "origin/" + base
			if _, err := runCmd(root, "git", "rev-parse", "--verify", "--quiet", ref); err == nil {
				return ref + "..HEAD"
			}
		}
	}
	return "@{upstream}..HEAD"
}

// loadCommits lists the commits of commitRange (fallback: the last 25)
// and parses each commit's diff.
func loadCommits(root string, ctx int) ([]*Commit, error) {
	format := "--format=%H%x1f%s%x1e"
	out, err := runCmd(root, "git", "log", "--no-merges", format, commitRange(root))
	if err != nil || strings.TrimSpace(out) == "" {
		out, err = runCmd(root, "git", "log", "--no-merges", "-25", format, "HEAD")
		if err != nil {
			return nil, err
		}
	}
	var commits []*Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x1f", 2)
		if len(parts) != 2 {
			continue
		}
		c := &Commit{Hash: parts[0], Subject: parts[1]}
		showOut, err := runCmd(root, "git", "show", "--format=",
			fmt.Sprintf("-U%d", ctx), "--no-color", "--no-ext-diff", c.Hash)
		if err != nil {
			return nil, err
		}
		files := parseUnifiedDiff(showOut)
		for _, f := range files {
			for _, h := range f.Hunks {
				h.Reviewable = true
				h.FilePath = f.Path
				h.Header = f.Path + "  " + h.Header
			}
			f.Staged = true
			assignIDs(f)
		}
		c.Files = files
		commits = append(commits, c)
	}
	return commits, nil
}

type CommitList struct {
	commits []*Commit
	cursor  int
	scroll  int
}

func rebuildCommitList(old *CommitList, commits []*Commit) *CommitList {
	cl := &CommitList{commits: commits}
	if old != nil {
		if c := old.Current(); c != nil {
			for i, nc := range commits {
				if nc.Hash == c.Hash {
					cl.cursor = i
					break
				}
			}
		}
	}
	return cl
}

func (cl *CommitList) Current() *Commit {
	if len(cl.commits) == 0 {
		return nil
	}
	return cl.commits[cl.cursor]
}

// Align scrolls so the cursor row sits at the top (pos -1), middle (0)
// or bottom (1) of the h rows tall view; the end may scroll past, blank
// lines fill the rest.
func (cl *CommitList) Align(h, pos int) {
	if h < 1 {
		h = 1
	}
	target := cl.cursor - h/2
	switch pos {
	case -1:
		target = cl.cursor
	case 1:
		target = cl.cursor - h + 1
	}
	cl.scroll = clamp(target, 0, cl.cursor)
}

func (cl *CommitList) Move(delta int) {
	if len(cl.commits) == 0 {
		return
	}
	cl.cursor = clamp(cl.cursor+delta, 0, len(cl.commits)-1)
}

// SearchJump moves the cursor to the next commit (cyclic) whose short
// hash or subject contains the query.
func (cl *CommitList) SearchJump(q string, dir int) bool {
	n := len(cl.commits)
	if n == 0 || q == "" {
		return false
	}
	lq := strings.ToLower(q)
	for step := 1; step <= n; step++ {
		i := ((cl.cursor+dir*step)%n + n) % n
		c := cl.commits[i]
		if strings.Contains(strings.ToLower(c.Short()+" "+c.Subject), lq) {
			cl.cursor = i
			return true
		}
	}
	return false
}

func (cl *CommitList) View(w, h int, store *Store, focused bool, query string) string {
	if len(cl.commits) == 0 {
		return stDim.Render("(no commits)")
	}
	if h < 1 {
		h = 1
	}
	if cl.cursor < cl.scroll {
		cl.scroll = cl.cursor
	}
	if cl.cursor >= cl.scroll+h {
		cl.scroll = cl.cursor - h + 1
	}
	var b strings.Builder
	end := min(len(cl.commits), cl.scroll+h)
	for i := cl.scroll; i < end; i++ {
		c := cl.commits[i]
		rev, tot := c.Counts(store)
		var st lipgloss.Style
		switch {
		case tot == 0:
			st = stDim
		case rev == tot && c.AnySkimmed(store):
			st = stSkimmed
		case rev == tot:
			st = stReviewed
		case rev > 0:
			st = stPartial
		default:
			st = stStaged
		}
		label := fmt.Sprintf("%s %s  %d/%d", c.Short(), c.Subject, rev, tot)
		label = truncate(expandTabs(label), w)
		if i == cl.cursor {
			st = st.Background(colCursorBg)
			if focused {
				st = st.Bold(true)
			}
			label = padRight(label, w)
		}
		hl := stSearch
		if i == cl.cursor {
			hl = stSearchCur // the match the cursor is on
		}
		b.WriteString(highlightMatches(label, query, st, hl))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
