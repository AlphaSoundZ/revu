package main

import (
	"fmt"
	"strings"
)

// runCheck asserts that every staged line is reviewed. It returns the
// report and true when the assertion holds (also when nothing is staged).
func runCheck(root string, store *Store) (string, bool) {
	files, err := loadLocal(root, 0)
	if err != nil {
		return "revu: " + err.Error(), false
	}
	cfg := loadConfig(root)
	for _, f := range files {
		f.Excluded = cfg.Excluded(f.Path)
	}
	rev, tot := 0, 0
	var missing []string
	for _, f := range files {
		r, t := f.Counts(store)
		rev += r
		tot += t
		if r < t {
			missing = append(missing, fmt.Sprintf("  %2s %s  %d/%d", f.StatusMarker(), f.Path, r, t))
		}
	}
	if tot == 0 {
		return "revu: nothing staged to review", true
	}
	if rev == tot {
		return fmt.Sprintf("revu: all %d staged lines reviewed", tot), true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "revu: %d/%d staged lines reviewed (%d%%) — unreviewed files:\n", rev, tot, rev*100/tot)
	b.WriteString(strings.Join(missing, "\n"))
	b.WriteString("\n\nrun `revu` to review, or `git commit --no-verify` to skip")
	return b.String(), false
}
