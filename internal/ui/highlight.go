package ui

import (
	"hash/fnv"
	"strconv"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Syntax highlighting for the code a replay types out.
//
// The output is ANSI-coloured text, which the reveal has to cut mid-line
// without splitting an escape sequence. visibleLen and truncateVisible already
// do that arithmetic, and a highlighted line has the same visible length as the
// plain one it came from, so the typing budget stays correct either way.

// highlightStyle is chosen to sit with the rest of the UI, which is a GitHub
// Dark palette.
const highlightStyle = "github-dark"

// hlCacheMax is how many highlighted files to keep. Replay revisits the same
// step on every tick, so without a cache a 33KB file would be lexed thirty
// times a second.
const hlCacheMax = 48

var hlCache = struct {
	sync.Mutex
	lines map[string][]string
	order []string
}{lines: make(map[string][]string)}

// highlightCode colours code for the terminal and splits it into lines.
//
// path is used to pick the language; it may be empty, in which case the content
// is analysed instead. Anything that cannot be lexed comes back as plain lines,
// so a caller never has to care whether highlighting succeeded.
func highlightCode(code, path string) []string {
	if code == "" {
		return nil
	}

	key := cacheKey(code, path)
	hlCache.Lock()
	if cached, ok := hlCache.lines[key]; ok {
		hlCache.Unlock()
		return cached
	}
	hlCache.Unlock()

	out := renderHighlight(code, path)

	hlCache.Lock()
	if _, exists := hlCache.lines[key]; !exists {
		hlCache.lines[key] = out
		hlCache.order = append(hlCache.order, key)
		// Evict oldest first. A replay moves forward, so the steps behind it
		// are the ones least likely to be needed again.
		for len(hlCache.order) > hlCacheMax {
			delete(hlCache.lines, hlCache.order[0])
			hlCache.order = hlCache.order[1:]
		}
	}
	hlCache.Unlock()

	return out
}

// renderHighlight does the actual lexing and colouring.
func renderHighlight(code, path string) []string {
	plain := strings.Split(code, "\n")

	lexer := pickLexer(code, path)
	if lexer == nil {
		return plain
	}
	style := styles.Get(highlightStyle)
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get(formatterName())
	if formatter == nil {
		return plain
	}

	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return plain
	}

	var buf strings.Builder
	if err := formatter.Format(&buf, style, it); err != nil {
		return plain
	}

	coloured := strings.Split(buf.String(), "\n")
	// The budget and the renderer must agree on how many lines there are. If
	// the formatter changed that, trust the plain text instead.
	if len(coloured) != len(plain) {
		return plain
	}
	return coloured
}

// formatterName matches the colour depth the terminal actually has. The rest of
// the UI is defined in hex, so a truecolor terminal gets truecolor here too;
// 256 colours washes these themes out, and a dumb terminal gets none.
func formatterName() string {
	switch lipgloss.ColorProfile() {
	case termenv.TrueColor:
		return "terminal16m"
	case termenv.ANSI256:
		return "terminal256"
	case termenv.ANSI:
		return "terminal16"
	}
	return "terminal256"
}

// pickLexer identifies the language: by filename first, since that is
// definitive, then by analysing the content for a heredoc with no target file.
func pickLexer(code, path string) chroma.Lexer {
	var lexer chroma.Lexer
	if path != "" {
		lexer = lexers.Match(path)
	}
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		return nil
	}
	// Coalescing merges adjacent tokens of the same type, which cuts the number
	// of escape sequences the reveal has to walk through.
	return chroma.Coalesce(lexer)
}

// cacheKey identifies a highlighted file cheaply. Hashing the content is far
// faster than lexing it, and the path is included because the same text can be
// highlighted differently depending on the language it is taken for.
func cacheKey(code, path string) string {
	h := fnv.New64a()
	h.Write([]byte(code))
	return path + "\x00" + strconv.FormatUint(h.Sum64(), 36)
}
