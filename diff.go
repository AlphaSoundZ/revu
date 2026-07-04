package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
)

// DLine is one line of a unified diff hunk.
type DLine struct {
	Origin byte // ' ', '+', '-'
	Text   string
	ID     string // stable content hash, set for +/- lines only
}

type Hunk struct {
	Header     string
	Lines      []DLine
	Reviewable bool   // true for staged hunks (local view) and all PR/commit hunks
	FilePath   string // set when hunks from several files share one entry (commit view)
}

// StatusMarker renders the tree status column: the M/A/D letter, prefixed
// with an extra "M" when the file has staged and unstaged changes at once.
func (f *FileEntry) StatusMarker() string {
	status := f.Status
	if status == 0 {
		status = 'M'
	}
	hasUnstaged := f.Untracked
	hasStaged := false
	for _, h := range f.Hunks {
		if h.Reviewable {
			hasStaged = true
		} else {
			hasUnstaged = true
		}
	}
	if hasStaged && hasUnstaged {
		return "M" + string(status)
	}
	return string(status)
}

// ChangedIdx returns the indices of the hunk's +/- lines.
func (h *Hunk) ChangedIdx() []int {
	var out []int
	for i, l := range h.Lines {
		if l.Origin == '+' || l.Origin == '-' {
			out = append(out, i)
		}
	}
	return out
}

type FileEntry struct {
	Path      string
	Hunks     []*Hunk
	Binary    bool
	Untracked bool
	Staged    bool // has at least one reviewable hunk
	Status    byte // 'M' modified, 'A' added, 'D' deleted
	Excluded  bool // matches an exclude pattern; ignored by review accounting
}

// parseUnifiedDiff parses `git diff` / `gh pr diff` output.
func parseUnifiedDiff(out string) []*FileEntry {
	var files []*FileEntry
	var cur *FileEntry
	var hunk *Hunk
	for _, ln := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			cur = &FileEntry{Status: 'M'}
			files = append(files, cur)
			hunk = nil
			rest := strings.TrimPrefix(ln, "diff --git ")
			if i := strings.Index(rest, " b/"); i >= 0 {
				cur.Path = rest[i+3:]
			}
		case cur == nil:
			continue
		case strings.HasPrefix(ln, "@@"):
			hunk = &Hunk{Header: ln}
			cur.Hunks = append(cur.Hunks, hunk)
		case hunk != nil:
			// empty context lines arrive as " "; a bare "" is only the
			// split artifact of the trailing newline — skip it
			if ln != "" && (ln[0] == ' ' || ln[0] == '+' || ln[0] == '-') {
				hunk.Lines = append(hunk.Lines, DLine{Origin: ln[0], Text: ln[1:]})
			}
			// "\ No newline at end of file" is skipped
		case strings.HasPrefix(ln, "+++ b/"):
			cur.Path = strings.TrimPrefix(ln, "+++ b/")
		case strings.HasPrefix(ln, "--- a/") && cur.Path == "":
			cur.Path = strings.TrimPrefix(ln, "--- a/")
		case strings.HasPrefix(ln, "Binary files"):
			cur.Binary = true
		case strings.HasPrefix(ln, "new file mode"):
			cur.Status = 'A'
		case strings.HasPrefix(ln, "deleted file mode"):
			cur.Status = 'D'
		}
	}
	var res []*FileEntry
	for _, f := range files {
		if f.Path != "" {
			res = append(res, f)
		}
	}
	return res
}

// assignIDs gives every changed line a content-addressed ID. The ID is a
// hash of (path, origin, text, nth-occurrence), so a line reviewed while
// staged keeps its mark when the same change later shows up in the PR diff.
func assignIDs(f *FileEntry) {
	occ := map[string]int{}
	for _, h := range f.Hunks {
		for i := range h.Lines {
			l := &h.Lines[i]
			if l.Origin != '+' && l.Origin != '-' {
				continue
			}
			key := string(l.Origin) + l.Text
			occ[key]++
			sum := sha1.Sum([]byte(fmt.Sprintf("%s\x00%c%s\x00%d", f.Path, l.Origin, l.Text, occ[key])))
			l.ID = hex.EncodeToString(sum[:])[:16]
		}
	}
}

// Counts returns reviewed/total changed lines across reviewable hunks.
// Excluded files count as 0/0 everywhere.
func (f *FileEntry) Counts(st *Store) (int, int) {
	if f.Excluded {
		return 0, 0
	}
	rev, tot := 0, 0
	for _, h := range f.Hunks {
		if !h.Reviewable {
			continue
		}
		for _, l := range h.Lines {
			if l.Origin == '+' || l.Origin == '-' {
				tot++
				if st.Has(l.ID) {
					rev++
				}
			}
		}
	}
	return rev, tot
}

// ToggleAllReviewed flips review state for every reviewable line of the
// file. Returns a status message when nothing could be toggled.
func (f *FileEntry) ToggleAllReviewed(st *Store) string {
	if f.Excluded {
		return "file is excluded from review (.revu/config.json)"
	}
	var ids []string
	all := true
	for _, h := range f.Hunks {
		if !h.Reviewable {
			continue
		}
		for _, l := range h.Lines {
			if l.Origin == '+' || l.Origin == '-' {
				ids = append(ids, l.ID)
				if !st.Has(l.ID) {
					all = false
				}
			}
		}
	}
	if len(ids) == 0 {
		return "only staged files can be marked as reviewed"
	}
	for _, id := range ids {
		st.Set(id, !all)
	}
	if err := st.Save(); err != nil {
		return "failed to save review state: " + err.Error()
	}
	return ""
}

func hunkCounts(h *Hunk, st *Store) (int, int) {
	rev, tot := 0, 0
	for _, l := range h.Lines {
		if l.Origin == '+' || l.Origin == '-' {
			tot++
			if st.Has(l.ID) {
				rev++
			}
		}
	}
	return rev, tot
}
