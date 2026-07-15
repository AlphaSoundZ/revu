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
	Staged    bool   // has at least one reviewable hunk (or is a staged binary)
	Status    byte   // 'M' modified, 'A' added, 'D' deleted
	Excluded  bool   // matches an exclude pattern; ignored by review accounting
	BlobID    string // new-side blob hash from the diff header (binaries)
	BinaryID  string // synthetic review ID for binary files
	FileID    string // whole-file review ID (FILES view), hashes path + content
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
		case strings.HasPrefix(ln, "Binary files"), strings.HasPrefix(ln, "GIT binary patch"):
			cur.Binary = true
		case strings.HasPrefix(ln, "index "):
			// "index <old>..<new> <mode>" — keep the new-side blob,
			// truncated so differing abbreviation widths still match
			rest := strings.TrimPrefix(ln, "index ")
			if i := strings.Index(rest, ".."); i >= 0 {
				blob := rest[i+2:]
				if j := strings.IndexByte(blob, ' '); j >= 0 {
					blob = blob[:j]
				}
				if len(blob) > 7 {
					blob = blob[:7]
				}
				cur.BlobID = blob
			}
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
// Binary files get one synthetic ID from path + blob hash instead.
func assignIDs(f *FileEntry) {
	if f.Binary {
		sum := sha1.Sum([]byte(f.Path + "\x00binary\x00" + f.BlobID))
		f.BinaryID = hex.EncodeToString(sum[:])[:16]
	}
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
// Excluded files count as 0/0 everywhere; a permanent mark on the file
// (or a parent folder) counts everything as reviewed.
func (f *FileEntry) Counts(st *Store) (int, int) {
	if f.Excluded {
		return 0, 0
	}
	rev, tot := 0, 0
	switch {
	case f.FileID != "":
		tot = 1
		if st.Has(f.FileID) {
			rev = 1
		}
	case f.Binary:
		if !f.Staged || f.BinaryID == "" {
			return 0, 0
		}
		tot = 1
		if st.Has(f.BinaryID) {
			rev = 1
		}
	default:
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
	}
	if _, ok := st.Permanent(f.Path); ok {
		rev = tot
	}
	return rev, tot
}

// ToggleAllReviewed flips review state for every reviewable line of the
// file; skim toggles the skimmed mark instead. Returns a status message
// when nothing could be toggled.
func (f *FileEntry) ToggleAllReviewed(st *Store, skim bool) string {
	if f.Excluded {
		return "file is excluded from review (.revu/config.json)"
	}
	ids := reviewableIDs(f)
	if len(ids) == 0 {
		return "only staged files can be marked as reviewed"
	}
	all := true
	for _, id := range ids {
		if !st.In(id, skim) {
			all = false
			break
		}
	}
	for _, id := range ids {
		st.Mark(id, skim, !all)
	}
	if err := st.Save(); err != nil {
		return "failed to save review state: " + err.Error()
	}
	return ""
}

// reviewableIDs returns the ids space/S may toggle: the binary unit and
// the changed lines of reviewable hunks.
func reviewableIDs(f *FileEntry) []string {
	var ids []string
	if f.Binary && f.Staged && f.BinaryID != "" {
		ids = append(ids, f.BinaryID)
	}
	for _, h := range f.Hunks {
		if !h.Reviewable {
			continue
		}
		for _, l := range h.Lines {
			if l.Origin == '+' || l.Origin == '-' {
				ids = append(ids, l.ID)
			}
		}
	}
	return ids
}

// entryIDs returns every review ID an entry carries: the line IDs of all
// hunks (staged or not) plus the binary ID.
func entryIDs(e *FileEntry) []string {
	if e == nil {
		return nil
	}
	var ids []string
	if e.BinaryID != "" {
		ids = append(ids, e.BinaryID)
	}
	for _, h := range e.Hunks {
		for _, l := range h.Lines {
			if l.ID != "" {
				ids = append(ids, l.ID)
			}
		}
	}
	return ids
}

// AnySkimmed reports whether any counted line (or the binary unit) of
// the file is marked as skimmed.
func (f *FileEntry) AnySkimmed(st *Store) bool {
	if f.Excluded {
		return false
	}
	if skim, ok := st.Permanent(f.Path); ok {
		return skim // a permanent mark overrides line-level skims
	}
	if f.FileID != "" {
		return st.Skimmed(f.FileID)
	}
	if f.Binary {
		return f.Staged && st.Skimmed(f.BinaryID)
	}
	for _, h := range f.Hunks {
		if !h.Reviewable {
			continue
		}
		if hunkAnySkimmed(h, st) {
			return true
		}
	}
	return false
}

func hunkAnySkimmed(h *Hunk, st *Store) bool {
	for _, l := range h.Lines {
		if (l.Origin == '+' || l.Origin == '-') && st.Skimmed(l.ID) {
			return true
		}
	}
	return false
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
