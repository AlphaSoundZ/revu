package main

import (
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

var (
	synFormatter = formatters.Get("terminal16m")
	synStyle     = styles.Get("catppuccin-mocha")
	synLexers    = map[string]chroma.Lexer{}
)

// lexerFor returns a coalesced lexer for the file, nil when chroma has
// none. Lexers are cached per extension (or basename for extensionless
// files like Makefile).
func lexerFor(path string) chroma.Lexer {
	key := strings.ToLower(filepath.Ext(path))
	if key == "" {
		key = strings.ToLower(filepath.Base(path))
	}
	if lx, ok := synLexers[key]; ok {
		return lx
	}
	lx := lexers.Match(filepath.Base(path))
	if lx != nil {
		lx = chroma.Coalesce(lx)
	}
	synLexers[key] = lx
	return lx
}

// highlightLine renders one source line with ANSI syntax colors. Lines
// are lexed individually, so multi-line constructs (block comments, raw
// strings) may color slightly off — an acceptable trade-off for a diff.
func highlightLine(path, line string) (string, bool) {
	lx := lexerFor(path)
	if lx == nil {
		return "", false
	}
	it, err := lx.Tokenise(nil, line)
	if err != nil {
		return "", false
	}
	var b strings.Builder
	if err := synFormatter.Format(&b, synStyle, it); err != nil {
		return "", false
	}
	return strings.TrimRight(b.String(), "\n"), true
}
