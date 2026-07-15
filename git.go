package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
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

// loadFiles lists every file in the repository (tracked and untracked,
// .gitignore respected) as whole-file review units for the FILES view.
// The review ID hashes path + blob content, so a mark expires as soon as
// the file changes — the same content-addressing the line marks use.
func loadFiles(root string) ([]*FileEntry, error) {
	out, err := runCmd(root, "git", "ls-files", "-s")
	if err != nil {
		return nil, err
	}
	blob := map[string]string{}
	var order []string
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if ln == "" {
			continue
		}
		// "<mode> <hash> <stage>\t<path>"
		tab := strings.IndexByte(ln, '\t')
		if tab < 0 {
			continue
		}
		meta := strings.Fields(ln[:tab])
		// skip conflict stages and submodule gitlinks
		if len(meta) != 3 || meta[2] != "0" || meta[0] == "160000" {
			continue
		}
		path := ln[tab+1:]
		if _, ok := blob[path]; !ok {
			order = append(order, path)
		}
		blob[path] = meta[1]
	}
	// worktree differs from the index: the on-disk content is what gets
	// reviewed, so hash that instead of the index blob
	dirty := map[string]bool{}
	if dOut, err := runCmd(root, "git", "diff", "--name-only"); err == nil {
		for _, p := range strings.Split(strings.TrimSpace(dOut), "\n") {
			if p != "" {
				dirty[p] = true
			}
		}
	}
	untracked := map[string]bool{}
	if utOut, err := runCmd(root, "git", "ls-files", "--others", "--exclude-standard"); err == nil {
		for _, p := range strings.Split(strings.TrimSpace(utOut), "\n") {
			if p == "" {
				continue
			}
			if _, ok := blob[p]; !ok {
				order = append(order, p)
			}
			blob[p] = ""
			dirty[p] = true
			untracked[p] = true
		}
	}
	sort.Strings(order)
	var files []*FileEntry
	for _, p := range order {
		h := blob[p]
		if dirty[p] {
			var err error
			if h, err = blobHash(filepath.Join(root, p)); err != nil {
				continue // deleted from the worktree
			}
		}
		status := byte(' ')
		switch {
		case untracked[p]:
			status = 'A'
		case dirty[p]:
			status = 'M'
		}
		e := &FileEntry{Path: p, Status: status, Staged: true}
		sum := sha1.Sum([]byte(p + "\x00file\x00" + h))
		e.FileID = hex.EncodeToString(sum[:])[:16]
		files = append(files, e)
	}
	return files, nil
}

// fileHistoryIDs collects the review IDs of every past change to the
// file. Each commit's diff is parsed separately so the occurrence
// counting — and therefore the IDs — match the scheme loadCommits uses.
// The IDs hash only changed lines, so -U0 is safe and fast. Errors (e.g.
// an empty repository) just mean no history.
func fileHistoryIDs(root, path string) []string {
	out, err := runCmd(root, "git", "log", "--no-merges", "--format=%x1e",
		"-p", "-U0", "--no-color", "--no-ext-diff", "--", path)
	if err != nil {
		return nil
	}
	var ids []string
	for _, chunk := range strings.Split(out, "\x1e") {
		for _, f := range parseUnifiedDiff(chunk) {
			assignIDs(f)
			ids = append(ids, entryIDs(f)...)
		}
	}
	return ids
}

// blobHash computes git's blob hash of the file's on-disk content.
func blobHash(abs string) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ensurePreview lazily loads a file's content as a context-only hunk so
// the FILES view can show it in the diff pane.
func ensurePreview(root string, e *FileEntry) {
	if e == nil || e.FileID == "" || e.Binary || len(e.Hunks) > 0 {
		return
	}
	data, err := os.ReadFile(filepath.Join(root, e.Path))
	if err != nil {
		return
	}
	if bytes.IndexByte(data, 0) >= 0 {
		e.Binary = true
		return
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) > 5000 {
		lines = lines[:5000]
	}
	h := &Hunk{Header: fmt.Sprintf("@@ %s (%d lines) @@", e.Path, len(lines))}
	for _, l := range lines {
		h.Lines = append(h.Lines, DLine{Origin: ' ', Text: l})
	}
	e.Hunks = []*Hunk{h}
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
