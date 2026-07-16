package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Store persists review marks in <repo>/.revu/reviewed.json. A line is
// either reviewed (read closely) or skimmed (read over and plausible,
// but not written by the reviewer) — never both at once. Permanent
// marks are path-based (files or folders) and survive content changes.
type Store struct {
	dir      string
	Lines    map[string]bool `json:"reviewedLines"`
	Skim     map[string]bool `json:"skimmedLines"`
	PermRev  map[string]bool `json:"permanentReviewed"`
	PermSkim map[string]bool `json:"permanentSkimmed"`
}

func LoadStore(root string) (*Store, error) {
	dir := filepath.Join(root, ".revu")
	s := &Store{dir: dir}
	if data, err := os.ReadFile(filepath.Join(dir, "reviewed.json")); err == nil {
		_ = json.Unmarshal(data, s)
	}
	if s.Lines == nil {
		s.Lines = map[string]bool{}
	}
	if s.Skim == nil {
		s.Skim = map[string]bool{}
	}
	if s.PermRev == nil {
		s.PermRev = map[string]bool{}
	}
	if s.PermSkim == nil {
		s.PermSkim = map[string]bool{}
	}
	return s, nil
}

// ensureRevuDir creates <repo>/.revu with a .gitignore that hides it.
func ensureRevuDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	gi := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gi); err != nil {
		_ = os.WriteFile(gi, []byte("*\n"), 0o644)
	}
	return nil
}

func (s *Store) Save() error {
	if err := ensureRevuDir(s.dir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, "reviewed.json"), data, 0o644)
}

// Has reports whether the line carries any mark (reviewed or skimmed);
// both count as done for progress and `revu check`.
func (s *Store) Has(id string) bool {
	return id != "" && (s.Lines[id] || s.Skim[id])
}

// Skimmed reports whether the line is marked as skimmed.
func (s *Store) Skimmed(id string) bool {
	return id != "" && s.Skim[id]
}

// In reports whether the line is exactly in the given state.
func (s *Store) In(id string, skim bool) bool {
	if id == "" {
		return false
	}
	if skim {
		return s.Skim[id]
	}
	return s.Lines[id]
}

// Mark puts the line into the given state (v=true) or clears every mark
// (v=false); reviewed and skimmed are mutually exclusive.
func (s *Store) Mark(id string, skim, v bool) {
	if id == "" {
		return
	}
	delete(s.Lines, id)
	delete(s.Skim, id)
	if !v {
		return
	}
	if skim {
		s.Skim[id] = true
	} else {
		s.Lines[id] = true
	}
}

// Set marks or clears a line as reviewed.
func (s *Store) Set(id string, v bool) {
	s.Mark(id, false, v)
}

// Permanent reports whether a permanent mark covers the path — set on
// the path itself or inherited from a parent folder. skim tells which.
func (s *Store) Permanent(path string) (skim, ok bool) {
	for p := path; ; {
		if s.PermSkim[p] {
			return true, true
		}
		if s.PermRev[p] {
			return false, true
		}
		i := strings.LastIndexByte(p, '/')
		if i < 0 {
			return false, false
		}
		p = p[:i]
	}
}

// PermanentAt reports whether the exact path carries the given
// permanent mark (no folder inheritance).
func (s *Store) PermanentAt(path string, skim bool) bool {
	if skim {
		return s.PermSkim[path]
	}
	return s.PermRev[path]
}

// SetPermanent sets (v=true) or clears (v=false) the permanent mark on
// the exact path; reviewed and skimmed are mutually exclusive.
func (s *Store) SetPermanent(path string, skim, v bool) {
	delete(s.PermRev, path)
	delete(s.PermSkim, path)
	if !v {
		return
	}
	if skim {
		s.PermSkim[path] = true
	} else {
		s.PermRev[path] = true
	}
}
