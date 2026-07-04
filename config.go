package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config is read from <repo>/.revu/config.json. Files matching an exclude
// pattern are ignored by the review accounting (percentage, tree colors)
// and cannot be marked as reviewed.
type Config struct {
	Exclude []string `json:"exclude"`
}

var defaultExclude = []string{"snapshot.json"}

func loadConfig(root string) Config {
	cfg := Config{Exclude: defaultExclude}
	data, err := os.ReadFile(filepath.Join(root, ".revu", "config.json"))
	if err != nil {
		return cfg
	}
	var c Config
	if json.Unmarshal(data, &c) == nil && c.Exclude != nil {
		cfg.Exclude = c.Exclude
	}
	return cfg
}

// Excluded reports whether a repo-relative path matches an exclude
// pattern. Patterns with a slash match the full path, others the base
// name (glob syntax, e.g. "*.snap" or "generated/*").
func (c Config) Excluded(path string) bool {
	base := filepath.Base(path)
	for _, p := range c.Exclude {
		if strings.Contains(p, "/") {
			if ok, _ := filepath.Match(p, path); ok {
				return true
			}
		} else if ok, _ := filepath.Match(p, base); ok {
			return true
		}
	}
	return false
}
