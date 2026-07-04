package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Store persists reviewed-line IDs in <repo>/.revu/reviewed.json.
type Store struct {
	dir   string
	Lines map[string]bool `json:"reviewedLines"`
}

func LoadStore(root string) (*Store, error) {
	dir := filepath.Join(root, ".revu")
	s := &Store{dir: dir, Lines: map[string]bool{}}
	if data, err := os.ReadFile(filepath.Join(dir, "reviewed.json")); err == nil {
		_ = json.Unmarshal(data, s)
		if s.Lines == nil {
			s.Lines = map[string]bool{}
		}
	}
	return s, nil
}

func (s *Store) Save() error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	gi := filepath.Join(s.dir, ".gitignore")
	if _, err := os.Stat(gi); err != nil {
		_ = os.WriteFile(gi, []byte("*\n"), 0o644)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, "reviewed.json"), data, 0o644)
}

func (s *Store) Has(id string) bool {
	return id != "" && s.Lines[id]
}

func (s *Store) Set(id string, v bool) {
	if id == "" {
		return
	}
	if v {
		s.Lines[id] = true
	} else {
		delete(s.Lines, id)
	}
}
