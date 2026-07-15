package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type Node struct {
	Name     string
	Path     string
	IsDir    bool
	Expanded bool
	Children []*Node
	Entry    *FileEntry
}

func buildTree(files []*FileEntry, expanded map[string]bool) *Node {
	root := &Node{IsDir: true, Expanded: true}
	for _, f := range files {
		parts := strings.Split(f.Path, "/")
		cur := root
		for i, part := range parts {
			last := i == len(parts)-1
			var child *Node
			for _, c := range cur.Children {
				if c.Name == part && c.IsDir == !last {
					child = c
					break
				}
			}
			if child == nil {
				child = &Node{Name: part, Path: strings.Join(parts[:i+1], "/"), IsDir: !last}
				if child.IsDir {
					if exp, ok := expanded[child.Path]; ok {
						child.Expanded = exp
					} else {
						child.Expanded = true
					}
				}
				cur.Children = append(cur.Children, child)
			}
			if last {
				child.Entry = f
			}
			cur = child
		}
	}
	sortNode(root)
	return root
}

func sortNode(n *Node) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return a.Name < b.Name
	})
	for _, c := range n.Children {
		sortNode(c)
	}
}

// entriesUnder collects the file entries of a node's subtree (or the
// node's own entry for a file).
func entriesUnder(n *Node) []*FileEntry {
	if !n.IsDir {
		if n.Entry == nil {
			return nil
		}
		return []*FileEntry{n.Entry}
	}
	var out []*FileEntry
	for _, c := range n.Children {
		out = append(out, entriesUnder(c)...)
	}
	return out
}

// nodeCounts aggregates reviewed/total reviewable lines over a subtree.
func nodeCounts(n *Node, store *Store) (int, int) {
	if !n.IsDir {
		if n.Entry == nil {
			return 0, 0
		}
		return n.Entry.Counts(store)
	}
	rev, tot := 0, 0
	for _, c := range n.Children {
		r, t := nodeCounts(c, store)
		rev += r
		tot += t
	}
	return rev, tot
}

// nodeAnySkimmed reports whether any counted line in the subtree is
// marked as skimmed.
func nodeAnySkimmed(n *Node, store *Store) bool {
	if !n.IsDir {
		return n.Entry != nil && n.Entry.AnySkimmed(store)
	}
	for _, c := range n.Children {
		if nodeAnySkimmed(c, store) {
			return true
		}
	}
	return false
}

type treeRow struct {
	node  *Node
	depth int
}

type TreeModel struct {
	root   *Node
	rows   []treeRow
	cursor int
	scroll int
}

func NewTree(files []*FileEntry, expanded map[string]bool) *TreeModel {
	t := &TreeModel{root: buildTree(files, expanded)}
	t.flatten()
	return t
}

func (t *TreeModel) flatten() {
	t.rows = nil
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		for _, c := range n.Children {
			t.rows = append(t.rows, treeRow{c, depth})
			if c.IsDir && c.Expanded {
				walk(c, depth+1)
			}
		}
	}
	walk(t.root, 0)
	if t.cursor >= len(t.rows) {
		t.cursor = len(t.rows) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
}

func (t *TreeModel) Current() *Node {
	if len(t.rows) == 0 {
		return nil
	}
	return t.rows[t.cursor].node
}

func (t *TreeModel) Move(delta int) {
	if len(t.rows) == 0 {
		return
	}
	t.cursor = clamp(t.cursor+delta, 0, len(t.rows)-1)
}

func (t *TreeModel) SelectPath(p string) {
	for i, r := range t.rows {
		if r.node.Path == p {
			t.cursor = i
			return
		}
	}
}

// SearchJump moves the cursor to the next node (cyclic) whose path
// contains the query, expanding collapsed ancestors as needed.
func (t *TreeModel) SearchJump(q string, dir int) bool {
	if q == "" {
		return false
	}
	var nodes []*Node
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			nodes = append(nodes, c)
			if c.IsDir {
				walk(c)
			}
		}
	}
	walk(t.root)
	if len(nodes) == 0 {
		return false
	}
	lq := strings.ToLower(q)
	curIdx := -1
	if cur := t.Current(); cur != nil {
		for i, n := range nodes {
			if n == cur {
				curIdx = i
				break
			}
		}
	}
	n := len(nodes)
	for step := 1; step <= n; step++ {
		i := ((curIdx+dir*step)%n + n) % n
		if strings.Contains(strings.ToLower(nodes[i].Path), lq) {
			t.reveal(nodes[i])
			return true
		}
	}
	return false
}

// reveal expands all ancestors of the node and puts the cursor on it.
func (t *TreeModel) reveal(target *Node) {
	parts := strings.Split(target.Path, "/")
	cur := t.root
	for i := 0; i < len(parts)-1; i++ {
		p := strings.Join(parts[:i+1], "/")
		var next *Node
		for _, c := range cur.Children {
			if c.IsDir && c.Path == p {
				next = c
				break
			}
		}
		if next == nil {
			return
		}
		next.Expanded = true
		cur = next
	}
	t.flatten()
	for i, r := range t.rows {
		if r.node == target {
			t.cursor = i
			return
		}
	}
}

// CollapseAll folds every folder, then re-expands the named top-level
// paths. The FILES view starts this way: everything closed, app/ open.
func (t *TreeModel) CollapseAll(expand ...string) {
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			if c.IsDir {
				c.Expanded = false
				walk(c)
			}
		}
	}
	walk(t.root)
	for _, p := range expand {
		for _, c := range t.root.Children {
			if c.IsDir && c.Path == p {
				c.Expanded = true
			}
		}
	}
	t.flatten()
}

func (t *TreeModel) ExpandedMap() map[string]bool {
	m := map[string]bool{}
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			if c.IsDir {
				m[c.Path] = c.Expanded
				walk(c)
			}
		}
	}
	walk(t.root)
	return m
}

func (t *TreeModel) View(w, h int, store *Store, focused bool, query string) string {
	if len(t.rows) == 0 {
		return stDim.Render("(no files)")
	}
	if h < 1 {
		h = 1
	}
	if t.cursor < t.scroll {
		t.scroll = t.cursor
	}
	if t.cursor >= t.scroll+h {
		t.scroll = t.cursor - h + 1
	}
	var b strings.Builder
	end := min(len(t.rows), t.scroll+h)
	for i := t.scroll; i < end; i++ {
		r := t.rows[i]
		prefix := strings.Repeat("  ", r.depth)
		var st lipgloss.Style
		var label string
		if r.node.IsDir {
			arrow := "▸"
			if r.node.Expanded {
				arrow = "▾"
			}
			rev, tot := nodeCounts(r.node, store)
			st = dirStyle(rev, tot, nodeAnySkimmed(r.node, store))
			label = fmt.Sprintf("%s%s %s/", prefix, arrow, r.node.Name)
		} else {
			e := r.node.Entry
			rev, tot := e.Counts(store)
			st = fileStyle(e, rev, tot, e.AnySkimmed(store))
			suffix := ""
			if tot > 0 && e.FileID == "" { // whole-file units: the color says it all
				suffix = fmt.Sprintf("  %d/%d", rev, tot)
			}
			label = fmt.Sprintf("%s%2s %s%s", prefix, e.StatusMarker(), r.node.Name, suffix)
		}
		if _, ok := store.Permanent(r.node.Path); ok {
			label += " ∞" // permanently marked (set here or on a parent)
		}
		label = truncate(expandTabs(label), w)
		if i == t.cursor {
			st = st.Background(colCursorBg)
			if focused {
				st = st.Bold(true)
			}
			label = padRight(label, w)
		}
		hl := stSearch
		if i == t.cursor {
			hl = stSearchCur // the match the cursor is on
		}
		b.WriteString(highlightMatches(label, query, st, hl))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
