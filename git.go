package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func runCmd(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

func runCmdInput(dir, input, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(input)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

func stagePath(root, path string) error {
	_, err := runCmd(root, "git", "add", "-A", "--", path)
	return err
}

func unstagePath(root, path string) error {
	_, err := runCmd(root, "git", "restore", "--staged", "--", path)
	return err
}

type hunkPick struct {
	hunk  *Hunk
	lines map[int]bool // indices into hunk.Lines to (un)stage
}

// buildPatch builds a partial patch for the selected lines. Staging:
// unselected '-' become context, unselected '+' are dropped. Unstaging
// (reverse apply): unselected '+' become context, unselected '-' are
// dropped. Counts are recomputed either way.
func buildPatch(path string, picks []hunkPick, unstage bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n", path, path, path, path)
	hunks := 0
	for _, p := range picks {
		oldStart := parseOldStart(p.hunk.Header)
		newStart := parseNewStart(p.hunk.Header)
		if oldStart == 0 && newStart == 0 {
			continue
		}
		var body strings.Builder
		oldCnt, newCnt, changes := 0, 0, 0
		ctx := func(text string) {
			body.WriteString(" " + text + "\n")
			oldCnt++
			newCnt++
		}
		for i, l := range p.hunk.Lines {
			switch l.Origin {
			case ' ':
				ctx(l.Text)
			case '-':
				switch {
				case p.lines[i]:
					body.WriteString("-" + l.Text + "\n")
					oldCnt++
					changes++
				case !unstage:
					ctx(l.Text)
					// when unstaging, unselected '-' lines are not in the
					// index; drop them
				}
			case '+':
				switch {
				case p.lines[i]:
					body.WriteString("+" + l.Text + "\n")
					newCnt++
					changes++
				case unstage:
					ctx(l.Text)
					// when staging, unselected '+' lines are not in the
					// index yet; drop them
				}
			}
		}
		if changes == 0 {
			continue
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCnt, newStart, newCnt)
		b.WriteString(body.String())
		hunks++
	}
	if hunks == 0 {
		return ""
	}
	return b.String()
}

func stageLines(root, path string, picks []hunkPick) error {
	patch := buildPatch(path, picks, false)
	if patch == "" {
		return fmt.Errorf("nothing to stage")
	}
	_, err := runCmdInput(root, patch, "git", "apply", "--cached", "--whitespace=nowarn", "-")
	return err
}

func unstageLines(root, path string, picks []hunkPick) error {
	patch := buildPatch(path, picks, true)
	if patch == "" {
		return fmt.Errorf("nothing to unstage")
	}
	_, err := runCmdInput(root, patch, "git", "apply", "--cached", "--reverse", "--whitespace=nowarn", "-")
	return err
}

func repoRoot() (string, error) {
	out, err := runCmd(".", "git", "rev-parse", "--show-toplevel")
	return strings.TrimSpace(out), err
}

// loadLocal merges staged diff, unstaged diff and untracked files into one
// entry per path. Only staged hunks are reviewable. ctx is the number of
// unchanged context lines around each change (git -U<n>).
func loadLocal(root string, ctx int) ([]*FileEntry, error) {
	u := fmt.Sprintf("-U%d", ctx)
	stagedOut, err := runCmd(root, "git", "diff", "--cached", u, "--no-color", "--no-ext-diff")
	if err != nil {
		return nil, err
	}
	unstagedOut, err := runCmd(root, "git", "diff", u, "--no-color", "--no-ext-diff")
	if err != nil {
		return nil, err
	}
	byPath := map[string]*FileEntry{}
	var order []string
	add := func(fs []*FileEntry, reviewable bool) {
		for _, f := range fs {
			for _, h := range f.Hunks {
				h.Reviewable = reviewable
			}
			e, ok := byPath[f.Path]
			if !ok {
				e = &FileEntry{Path: f.Path, Status: f.Status}
				byPath[f.Path] = e
				order = append(order, f.Path)
			} else if e.Status == 'M' && f.Status != 'M' && f.Status != 0 {
				e.Status = f.Status
			}
			e.Hunks = append(e.Hunks, f.Hunks...)
			if f.Binary {
				e.Binary = true
			}
			if e.BlobID == "" {
				e.BlobID = f.BlobID // staged side runs first and wins
			}
			if reviewable && (len(f.Hunks) > 0 || f.Binary) {
				e.Staged = true
			}
		}
	}
	add(parseUnifiedDiff(stagedOut), true)
	add(parseUnifiedDiff(unstagedOut), false)

	if utOut, err := runCmd(root, "git", "ls-files", "--others", "--exclude-standard"); err == nil {
		for _, p := range strings.Split(strings.TrimSpace(utOut), "\n") {
			if p == "" {
				continue
			}
			e := &FileEntry{Path: p, Untracked: true, Status: 'A'}
			if h, binary := untrackedHunk(filepath.Join(root, p)); binary {
				e.Binary = true
			} else if h != nil {
				e.Hunks = []*Hunk{h}
			}
			byPath[p] = e
			order = append(order, p)
		}
	}

	sort.Strings(order)
	var out []*FileEntry
	for _, p := range order {
		out = append(out, byPath[p])
	}
	for _, f := range out {
		assignIDs(f)
	}
	return out, nil
}

func untrackedHunk(abs string) (*Hunk, bool) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, false
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, true
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) > 5000 {
		lines = lines[:5000]
	}
	h := &Hunk{Header: fmt.Sprintf("@@ untracked (%d lines) @@", len(lines))}
	for _, l := range lines {
		h.Lines = append(h.Lines, DLine{Origin: '+', Text: l})
	}
	return h, false
}

type PRInfo struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	BaseRefName string `json:"baseRefName"`
}

// loadPR fetches the diff of the PR for the current branch. The diff is
// computed locally against origin/<base> so the context width (-U<n>) can
// be applied; `gh pr diff` (fixed 3-line context) is the fallback when the
// base ref is not available locally. Every hunk is reviewable here.
func loadPR(root string, ctx int) ([]*FileEntry, *PRInfo, error) {
	metaOut, err := runCmd(root, "gh", "pr", "view", "--json", "number,title,baseRefName")
	if err != nil {
		return nil, nil, fmt.Errorf("no open PR for this branch (%v)", err)
	}
	var info PRInfo
	if err := json.Unmarshal([]byte(metaOut), &info); err != nil {
		return nil, nil, err
	}
	var diffOut string
	if info.BaseRefName != "" {
		out, lerr := runCmd(root, "git", "diff", fmt.Sprintf("-U%d", ctx),
			"--no-color", "--no-ext-diff", "origin/"+info.BaseRefName+"...HEAD")
		if lerr == nil {
			diffOut = out
		}
	}
	if diffOut == "" {
		diffOut, err = runCmd(root, "gh", "pr", "diff")
		if err != nil {
			return nil, nil, err
		}
	}
	files := parseUnifiedDiff(diffOut)
	for _, f := range files {
		for _, h := range f.Hunks {
			h.Reviewable = true
		}
		f.Staged = true
		assignIDs(f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, &info, nil
}
