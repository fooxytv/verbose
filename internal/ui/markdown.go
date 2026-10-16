package ui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

// Markdown in a replay.
//
// A plan step is the agent's own prose, and an agent writes markdown. Over the
// 2,327 assistant text blocks on one machine, 53% carry inline code, 34% bold,
// 17% a heading, 15% a fenced block, 11% bullets and 10% a table — and a third
// of them are more than one line. DescribeEvent used to put every one of those
// through collapseSpaces, which is strings.Fields joined by a space: it deletes
// every newline, so a heading, the list under it and a table all ran together
// into one unbroken paragraph. That is what this file fixes.
//
// Only plan steps come through here. Every other step's Detail is a sentence
// this program generated — it has no markup in it, and putting generated text
// through a markdown parser can only misread it.
//
// The width contract is wrapProse's: a returned line is prefix plus at most
// maxWidth columns, measured the way visibleLen measures. Nothing is clamped
// on the way out, so a mistake here shows up in the width tests instead of
// being quietly hidden by clampWidth.

const (
	// Both measured with runewidth: width 1 and NOT ambiguous-width, unlike
	// •, ·, ─ and │, which a terminal configured to draw ambiguous glyphs wide
	// renders in two columns. visibleLen counts runes, not columns, so an
	// ambiguous glyph is a layout bug waiting for the wrong terminal.
	mdRule     = "╌" // U+254C — horizontal rule, and a table's header divider
	mdQuoteBar = "▐" // U+2590 — the gutter down the left of a block quote
)

// mdBullets are the list markers by nesting depth: ▪ U+25AA, ◦ U+25E6,
// ‣ U+2023. All three measured width 1 and non-ambiguous.
var mdBullets = []string{"▪", "◦", "‣"}

// mdMaxDepth is as far as nesting is tracked. Deeper lists exist in theory;
// 11 of 2,327 blocks had a nested bullet at all, so the third level is already
// generous and a fourth would only eat the text's width.
const mdMaxDepth = 2

var (
	// A heading is the one thing that should catch the eye in a wall of prose,
	// so the top two levels take the UI's blue. Deeper ones are section labels
	// inside an argument and only need weight.
	mdHeadingStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	mdSubHeadingStyle = lipgloss.NewStyle().Bold(true).Foreground(colorText)

	// Inline code is the most common markup of all, and orange is the one
	// colour not already spoken for by a step kind.
	mdCodeStyle = lipgloss.NewStyle().Foreground(colorOrange)
	mdBoldStyle = lipgloss.NewStyle().Bold(true).Foreground(colorText)
	// 24% of blocks put code inside bold, so that pair needs a style of its
	// own: nesting one lipgloss style inside another ends the outer colour at
	// the inner reset and the rest of the line loses it.
	mdBoldCodeStyle = lipgloss.NewStyle().Bold(true).Foreground(colorOrange)
	mdItalicStyle   = lipgloss.NewStyle().Italic(true).Foreground(colorText)
	mdLinkStyle     = lipgloss.NewStyle().Underline(true).Foreground(colorBlue)
)

// mdInline is how one run of text is drawn. It is an enum rather than a
// lipgloss.Style so that two runs can be compared for equality while a word is
// being assembled — styles are structs with no usable comparison.
type mdInline int

const (
	mdPlain mdInline = iota
	mdCode
	mdBold
	mdBoldCode
	mdItalic
	mdLink
	mdURL
)

func (k mdInline) render(s string) string {
	switch k {
	case mdCode:
		return mdCodeStyle.Render(s)
	case mdBold:
		return mdBoldStyle.Render(s)
	case mdBoldCode:
		return mdBoldCodeStyle.Render(s)
	case mdItalic:
		return mdItalicStyle.Render(s)
	case mdLink:
		return mdLinkStyle.Render(s)
	case mdURL:
		return mutedStyle.Render(s)
	}
	return s
}

// mdSpan is a run of text in one style, with its markers already removed.
type mdSpan struct {
	text string
	kind mdInline
}

// renderMarkdown lays out a markdown block as styled terminal lines.
func renderMarkdown(src string, maxWidth int, prefix string) []string {
	if maxWidth <= 0 {
		maxWidth = 76
	}
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")

	var out []string

	// Consecutive text lines are ONE paragraph, as markdown says — this is the
	// half of the fix that collapseSpaces got backwards and wrapProse gets
	// backwards the other way: wrapProse treats every newline as a paragraph
	// break, so a soft-wrapped sentence came out as a column of short lines.
	var para []string
	flushPara := func() {
		if len(para) == 0 {
			return
		}
		text := strings.Join(para, " ")
		para = nil
		out = append(out, wrapWords(mdWords(parseInline(text)), maxWidth, prefix, prefix)...)
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		t := strings.TrimSpace(line)

		if lang, ok := mdFenceLang(t); ok {
			flushPara()
			body, end := mdCollectFence(lines, i)
			out = append(out, mdCodeLines(body, lang, maxWidth, prefix)...)
			i = end
			continue
		}

		if t == "" {
			flushPara()
			out = append(out, "")
			continue
		}

		// A table is only a table when the row after it is the delimiter. One
		// stray line beginning with a pipe is just prose.
		if mdIsTableRow(t) && i+1 < len(lines) && mdIsTableDivider(lines[i+1]) {
			flushPara()
			rows, end := mdCollectTable(lines, i)
			out = append(out, mdTableLines(rows, maxWidth, prefix)...)
			i = end
			continue
		}

		// Tested before the list, or "---" reads as a bullet with no text.
		if mdIsRule(t) {
			flushPara()
			out = append(out, prefix+mutedStyle.Render(strings.Repeat(mdRule, maxWidth)))
			continue
		}

		if level, text, ok := mdHeading(t); ok {
			flushPara()
			out = append(out, mdHeadingLines(level, text, maxWidth, prefix)...)
			continue
		}

		if strings.HasPrefix(t, ">") {
			flushPara()
			body, end := mdCollectQuote(lines, i)
			out = append(out, mdQuoteLines(body, maxWidth, prefix)...)
			i = end
			continue
		}

		if item, end, ok := mdCollectItem(lines, i); ok {
			flushPara()
			out = append(out, mdItemLines(item, maxWidth, prefix)...)
			i = end
			continue
		}

		para = append(para, t)
	}
	flushPara()

	return mdTidy(out)
}

// mdTidy drops the blank lines at either end and collapses a run of them to
// one. Rows are the scarcest thing in a pane, and the blank line a markdown
// source puts before a heading is already accounted for by the one after the
// paragraph above it.
func mdTidy(lines []string) []string {
	var out []string
	for _, l := range lines {
		blank := strings.TrimSpace(l) == ""
		if blank && (len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == "") {
			continue
		}
		out = append(out, l)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

// ---------------------------------------------------------------- inline

// parseInline strips the inline markers from a line and returns the text in
// styled runs.
func parseInline(s string) []mdSpan {
	return parseInlineAs(s, mdPlain)
}

// parseInlineAs is parseInline with an inherited style, which is how code
// inside bold comes out bold rather than resetting to plain orange.
func parseInlineAs(s string, base mdInline) []mdSpan {
	r := []rune(s)
	code := mdCode
	if base == mdBold {
		code = mdBoldCode
	}

	var out []mdSpan
	add := func(text string, kind mdInline) {
		if text == "" {
			return
		}
		// Merging keeps the number of escape sequences down, which matters
		// because truncateVisible walks every one of them.
		if n := len(out); n > 0 && out[n-1].kind == kind {
			out[n-1].text += text
			return
		}
		out = append(out, mdSpan{text: text, kind: kind})
	}

	var plain strings.Builder
	flush := func() {
		add(plain.String(), base)
		plain.Reset()
	}

	for i := 0; i < len(r); {
		switch {
		case r[i] == '`':
			if j := mdIndex(r, "`", i+1); j > i+1 {
				flush()
				add(string(r[i+1:j]), code)
				i = j + 1
				continue
			}

		case base != mdBold && r[i] == '*' && i+1 < len(r) && r[i+1] == '*':
			if j := mdIndex(r, "**", i+2); j > i+2 {
				flush()
				for _, sp := range parseInlineAs(string(r[i+2:j]), mdBold) {
					add(sp.text, sp.kind)
				}
				i = j + 2
				continue
			}

		case r[i] == '*' && mdEmphasisOpens(r, i):
			if j := mdEmphasisCloses(r, i+1); j > 0 {
				flush()
				add(string(r[i+1:j]), mdItalic)
				i = j + 1
				continue
			}

		case r[i] == '[':
			if text, url, next, ok := mdLinkAt(r, i); ok {
				flush()
				add(text, mdLink)
				// The URL is kept rather than swallowed: a terminal cannot be
				// clicked through, so dropping it would lose the only copy of
				// the address on screen.
				if url != "" && url != text {
					add(" ("+url+")", mdURL)
				}
				i = next
				continue
			}
		}

		plain.WriteRune(r[i])
		i++
	}
	flush()
	return out
}

// mdStripInline is the text of a line with every marker removed and no styling
// applied, for the places that style the whole line themselves.
func mdStripInline(s string) string {
	var b strings.Builder
	for _, sp := range parseInline(s) {
		b.WriteString(sp.text)
	}
	return b.String()
}

// mdIndex finds want in r at or after from, in rune offsets.
func mdIndex(r []rune, want string, from int) int {
	w := []rune(want)
	for i := from; i+len(w) <= len(r); i++ {
		if string(r[i:i+len(w)]) == want {
			return i
		}
	}
	return -1
}

// mdEmphasisOpens reports whether the asterisk at i can open emphasis.
//
// It has to sit at a word boundary and be followed by something other than a
// space. That is what keeps the asterisks in "rm *.go *.md" and "2*3*4"
// literal: the first is rejected at the close (see mdEmphasisCloses), the
// second here, because a digit is not a boundary. Underscores are never
// emphasis at all — snake_case is far more common in this text than _emphasis_
// and getting that wrong would eat half a identifier.
func mdEmphasisOpens(r []rune, i int) bool {
	if i+1 >= len(r) || unicode.IsSpace(r[i+1]) || r[i+1] == '*' {
		return false
	}
	if i == 0 {
		return true
	}
	switch p := r[i-1]; {
	case unicode.IsSpace(p), p == '(', p == '[', p == '"', p == '\'':
		return true
	}
	return false
}

// mdEmphasisCloses finds the asterisk that closes emphasis opened before from,
// or -1. The closing one must not be preceded by a space, which is what stops
// "rm *.go *.md" from italicising ".go ".
func mdEmphasisCloses(r []rune, from int) int {
	for j := from; j < len(r); j++ {
		if r[j] != '*' {
			continue
		}
		if unicode.IsSpace(r[j-1]) {
			continue
		}
		return j
	}
	return -1
}

// mdLinkAt reads a [text](url) starting at i.
func mdLinkAt(r []rune, i int) (text, url string, next int, ok bool) {
	close := mdIndex(r, "]", i+1)
	if close < 0 || close+1 >= len(r) || r[close+1] != '(' {
		return "", "", 0, false
	}
	end := mdIndex(r, ")", close+2)
	if end < 0 {
		return "", "", 0, false
	}
	text = string(r[i+1 : close])
	if strings.TrimSpace(text) == "" {
		return "", "", 0, false
	}
	return text, string(r[close+2 : end]), end + 1, true
}

// ---------------------------------------------------------------- wrapping

// mdPart is the run of one word that is drawn in a single style.
type mdPart struct {
	text string
	kind mdInline
}

// mdWord is one word. It is a list of parts because a style can change
// part-way through one — "**must**n't" is bold then plain with no space to
// break at — and a word is the unit the wrapper moves around.
type mdWord struct {
	parts []mdPart
	width int
}

// mdWords breaks styled spans into words, dropping the whitespace between
// them. A word that straddles two spans stays one word.
func mdWords(spans []mdSpan) []mdWord {
	var words []mdWord
	var cur mdWord
	flush := func() {
		if len(cur.parts) > 0 {
			words = append(words, cur)
			cur = mdWord{}
		}
	}

	for _, sp := range spans {
		rest := sp.text
		for rest != "" {
			if i := strings.IndexFunc(rest, func(r rune) bool { return !unicode.IsSpace(r) }); i != 0 {
				// Whitespace ends whatever word was being built.
				flush()
				if i < 0 {
					break
				}
				rest = rest[i:]
			}
			chunk := rest
			if j := strings.IndexFunc(rest, unicode.IsSpace); j >= 0 {
				chunk, rest = rest[:j], rest[j:]
			} else {
				rest = ""
			}
			cur.parts = append(cur.parts, mdPart{text: chunk, kind: sp.kind})
			cur.width += len([]rune(chunk))
		}
	}
	flush()
	return words
}

// wrapWords lays words out greedily within maxWidth columns.
//
// first and rest are the prefixes for the first line and every line after it.
// They must be the same visible width — a list item's marker and its hanging
// indent are built that way — because maxWidth is the budget left over after
// the prefix, and one shared budget is what keeps the arithmetic checkable.
func wrapWords(words []mdWord, maxWidth int, first, rest string) []string {
	if maxWidth < 1 {
		maxWidth = 1
	}

	var out []string
	var line strings.Builder
	n := 0

	prefix := func() string {
		if len(out) == 0 {
			return first
		}
		return rest
	}
	flush := func() {
		if n > 0 {
			out = append(out, prefix()+line.String())
			line.Reset()
			n = 0
		}
	}

	for _, w := range words {
		// A word wider than the column — a path, a URL, an unbroken identifier
		// — is broken rather than allowed to run off the edge.
		for w.width > maxWidth {
			flush()
			var head mdWord
			head, w = mdSplitWord(w, maxWidth)
			out = append(out, prefix()+mdRenderWord(head))
		}
		switch {
		case n == 0:
			line.WriteString(mdRenderWord(w))
			n = w.width
		case n+1+w.width <= maxWidth:
			line.WriteString(" " + mdRenderWord(w))
			n += 1 + w.width
		default:
			flush()
			line.WriteString(mdRenderWord(w))
			n = w.width
		}
	}
	flush()
	return out
}

// mdSplitWord cuts a word after at columns, keeping each piece's styling.
func mdSplitWord(w mdWord, at int) (head, tail mdWord) {
	for _, p := range w.parts {
		r := []rune(p.text)
		switch {
		case head.width >= at:
			tail.parts = append(tail.parts, p)
			tail.width += len(r)
		case head.width+len(r) <= at:
			head.parts = append(head.parts, p)
			head.width += len(r)
		default:
			cut := at - head.width
			head.parts = append(head.parts, mdPart{text: string(r[:cut]), kind: p.kind})
			head.width += cut
			tail.parts = append(tail.parts, mdPart{text: string(r[cut:]), kind: p.kind})
			tail.width += len(r) - cut
		}
	}
	return head, tail
}

// mdRenderWord draws a word, one escape sequence per style run.
func mdRenderWord(w mdWord) string {
	var b strings.Builder
	for _, p := range w.parts {
		b.WriteString(p.kind.render(p.text))
	}
	return b.String()
}

// ---------------------------------------------------------------- blocks

// mdHeading reads an ATX heading, returning its level and its text.
func mdHeading(t string) (int, string, bool) {
	level := 0
	for level < len(t) && t[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(t) || t[level] != ' ' {
		return 0, "", false
	}
	text := strings.TrimSpace(t[level+1:])
	if text == "" {
		return 0, "", false
	}
	return level, text, true
}

// mdHeadingLines draws a heading in one style across the whole line.
//
// A heading carries inline markup of its own ("## The `gh` trap"), but the
// whole line is already emphasised, so the markers are stripped and the
// heading's style applied to the result. Nesting the inline styles inside the
// heading's would end its colour at the first inner reset and leave the rest
// of the line plain.
func mdHeadingLines(level int, text string, maxWidth int, prefix string) []string {
	style := mdHeadingStyle
	if level >= 3 {
		style = mdSubHeadingStyle
	}
	words := mdWords([]mdSpan{{text: mdStripInline(text)}})

	var out []string
	for _, l := range wrapWords(words, maxWidth, "", "") {
		out = append(out, prefix+style.Render(l))
	}
	return out
}

// mdIsRule reports a thematic break: three or more of -, * or _ and nothing
// else.
func mdIsRule(t string) bool {
	if len(t) < 3 {
		return false
	}
	c := t[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	return strings.Trim(t, string(c)) == ""
}

// mdFenceLang reports whether a line opens a fenced code block, and what
// language it claims.
func mdFenceLang(t string) (string, bool) {
	for _, fence := range []string{"```", "~~~"} {
		if strings.HasPrefix(t, fence) {
			return strings.ToLower(strings.TrimSpace(strings.Trim(t, "`~ "))), true
		}
	}
	return "", false
}

// mdCollectFence reads a fenced block, returning its body and the index of the
// line the caller should resume after.
//
// The body keeps its own indentation but loses the fence's, so a block written
// inside a list item is not pushed off the right edge by its own nesting. An
// unterminated fence runs to the end of the text, which is what a reader wants
// from a truncated transcript.
func mdCollectFence(lines []string, i int) ([]string, int) {
	indent := mdIndent(lines[i])
	var body []string
	for j := i + 1; j < len(lines); j++ {
		if _, ok := mdFenceLang(strings.TrimSpace(lines[j])); ok {
			return body, j
		}
		body = append(body, mdUnindent(lines[j], indent))
	}
	return body, len(lines) - 1
}

// mdCodeLines highlights a fenced block and indents it clear of the prose.
func mdCodeLines(body []string, lang string, maxWidth int, prefix string) []string {
	code := strings.TrimRight(strings.Join(body, "\n"), "\n")
	if strings.TrimSpace(code) == "" {
		return nil
	}

	indent, budget := prefix+"  ", maxWidth-2
	if budget < 16 {
		// Too narrow to afford the indent; the code matters more than the gap.
		indent, budget = prefix, maxWidth
	}

	var out []string
	for _, l := range highlightCode(code, mdLangPath(lang)) {
		// Expand before cutting, or the cut is measured against a width the
		// terminal does not agree with: visibleLen counts a tab as one column
		// and a terminal draws up to eight.
		out = append(out, indent+truncateVisible(expandTabs(l), budget))
	}
	return out
}

// mdLangPath turns a fence's language into a filename chroma can match on.
// highlightCode picks a lexer by path and falls back to analysing the content,
// so an unknown language is not a failure — it is just a guess instead of a
// certainty.
func mdLangPath(lang string) string {
	switch lang {
	case "go", "golang":
		return "x.go"
	case "sh", "bash", "zsh", "shell", "console", "terminal":
		return "x.sh"
	case "py", "python":
		return "x.py"
	case "js", "javascript":
		return "x.js"
	case "ts", "typescript":
		return "x.ts"
	case "jsx", "tsx":
		return "x.tsx"
	case "json":
		return "x.json"
	case "yaml", "yml":
		return "x.yaml"
	case "toml":
		return "x.toml"
	case "hcl", "tf", "terraform":
		return "x.tf"
	case "sql":
		return "x.sql"
	case "md", "markdown":
		return "x.md"
	case "html":
		return "x.html"
	case "css":
		return "x.css"
	case "diff", "patch":
		return "x.diff"
	case "rs", "rust":
		return "x.rs"
	case "c":
		return "x.c"
	case "cpp", "c++":
		return "x.cpp"
	case "cs", "csharp":
		return "x.cs"
	case "java":
		return "x.java"
	case "rb", "ruby":
		return "x.rb"
	case "dockerfile", "docker":
		return "Dockerfile"
	case "make", "makefile":
		return "Makefile"
	case "xml":
		return "x.xml"
	case "ini", "conf":
		return "x.ini"
	}
	return ""
}

// mdCollectQuote reads a run of quoted lines and strips their markers.
func mdCollectQuote(lines []string, i int) ([]string, int) {
	var body []string
	j := i
	for ; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(t, ">") {
			break
		}
		body = append(body, strings.TrimSpace(strings.TrimPrefix(t, ">")))
	}
	return body, j - 1
}

// mdQuoteLines draws a quote behind a gutter.
//
// The text keeps the ordinary inline styles rather than being dimmed as a
// whole: one lipgloss style wrapped around already-styled text ends at the
// first inner reset, and the rest of the quote would come back to full
// brightness part-way through a line. The gutter is what marks it.
func mdQuoteLines(body []string, maxWidth int, prefix string) []string {
	text := strings.TrimSpace(strings.Join(body, " "))
	if text == "" {
		return nil
	}

	bar := mutedStyle.Render(mdQuoteBar) + " "
	budget := maxWidth - 2
	if budget < 8 {
		bar, budget = "", maxWidth
	}

	var out []string
	for _, l := range wrapWords(mdWords(parseInline(text)), budget, "", "") {
		out = append(out, prefix+bar+l)
	}
	return out
}

// mdItem is one list item: its marker, its text, and how deep it sits.
type mdItem struct {
	marker string
	text   string
	depth  int
}

// mdCollectItem reads a list item and any continuation lines belonging to it,
// returning the index to resume after.
//
// A space after the marker is required, which is what keeps a line opening
// "**Why:**" out of the bullet branch — and "---" never reaches here because a
// rule is tested first.
func mdCollectItem(lines []string, i int) (mdItem, int, bool) {
	indent := mdIndent(lines[i])
	t := strings.TrimSpace(lines[i])

	item := mdItem{depth: min(indent/2, mdMaxDepth)}
	switch {
	case len(t) > 1 && (t[0] == '-' || t[0] == '*' || t[0] == '+') && t[1] == ' ':
		item.marker = mdBullets[item.depth]
		item.text = strings.TrimSpace(t[2:])
	default:
		d := 0
		for d < len(t) && t[d] >= '0' && t[d] <= '9' {
			d++
		}
		if d == 0 || d+1 >= len(t) || (t[d] != '.' && t[d] != ')') || t[d+1] != ' ' {
			return mdItem{}, 0, false
		}
		// The number is kept as written: a reader following a numbered plan
		// needs "step 3" to say 3, even when the replay starts mid-list.
		item.marker = t[:d+1]
		item.text = strings.TrimSpace(t[d+2:])
	}

	// A continuation line is indented past the marker and is not the start of
	// something else. Without this a wrapped bullet would come out as its own
	// left-aligned paragraph directly under the one it belongs to.
	j := i + 1
	for ; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || mdIndent(lines[j]) <= indent {
			break
		}
		if mdStartsBlock(t) {
			break
		}
		if _, _, ok := mdCollectItem(lines, j); ok {
			break
		}
		item.text += " " + t
	}
	return item, j - 1, true
}

// mdStartsBlock reports whether a line opens a block that is not a paragraph,
// which is how a continuation line knows to stop.
func mdStartsBlock(t string) bool {
	if _, ok := mdFenceLang(t); ok {
		return true
	}
	if _, _, ok := mdHeading(t); ok {
		return true
	}
	return mdIsRule(t) || strings.HasPrefix(t, ">") || mdIsTableRow(t)
}

// mdItemLines draws a list item with its text hanging under itself.
func mdItemLines(item mdItem, maxWidth int, prefix string) []string {
	marker := len([]rune(item.marker))

	// Nesting gives way before the text does. A deep item in a narrow pane
	// would otherwise be indented into a column too thin to read.
	depth := item.depth
	for depth > 0 && maxWidth-2*depth-marker-1 < 24 {
		depth--
	}
	pad := strings.Repeat("  ", depth)
	budget := maxWidth - len(pad) - marker - 1
	if budget < 8 {
		// No room for a marker at all: fall back to a plain wrapped paragraph
		// rather than a one-character-wide column.
		return wrapWords(mdWords(parseInline(item.text)), maxWidth, prefix, prefix)
	}

	// first and rest are the same visible width, which is what wrapWords
	// needs: the marker is one column per rune and the indent matches it.
	first := prefix + pad + mutedStyle.Render(item.marker) + " "
	rest := prefix + pad + strings.Repeat(" ", marker+1)

	lines := wrapWords(mdWords(parseInline(item.text)), budget, first, rest)
	if len(lines) == 0 {
		// A bullet with no text is still a bullet; dropping it would lose a row
		// the author wrote.
		return []string{strings.TrimRight(first, " ")}
	}
	return lines
}

// ---------------------------------------------------------------- tables

// mdIsTableRow reports a line that could be a row of a table.
func mdIsTableRow(t string) bool {
	return strings.HasPrefix(t, "|")
}

// mdIsTableDivider reports the |---|---| row under a table's header. It is
// what distinguishes a table from a line of prose that happens to start with a
// pipe, so it is strict: pipes, dashes, colons and spaces only.
func mdIsTableDivider(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "|") || !strings.Contains(t, "-") {
		return false
	}
	return strings.Trim(t, "|-: ") == ""
}

// mdCollectTable reads a table, dropping the divider row, and returns the
// index to resume after.
func mdCollectTable(lines []string, i int) ([][]string, int) {
	var rows [][]string
	j := i
	for ; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if !mdIsTableRow(t) {
			break
		}
		if mdIsTableDivider(t) {
			continue
		}
		rows = append(rows, mdCells(t))
	}
	return rows, j - 1
}

// mdCells splits one row into its cells.
func mdCells(t string) []string {
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	cells := strings.Split(t, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// mdTableLines lays a table out in aligned columns.
//
// There are no vertical rules: a column of pipes down the screen is noise, and
// every box-drawing glyph that would draw them is ambiguous-width. Two spaces
// between columns and a bold header read as a table on their own.
func mdTableLines(rows [][]string, maxWidth int, prefix string) []string {
	if len(rows) == 0 {
		return nil
	}

	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	if cols == 0 {
		return nil
	}

	// Natural width of each column, then shrink the widest until the row fits.
	// Shrinking the widest first spreads the loss where there is most to lose,
	// rather than crushing a column of short values.
	const gap = 2
	widths := make([]int, cols)
	for _, r := range rows {
		for c, cell := range r {
			widths[c] = max(widths[c], len([]rune(mdStripInline(cell))))
		}
	}
	budget := maxWidth - gap*(cols-1)
	const minCol = 3
	for mdSum(widths) > budget {
		w, at := 0, -1
		for c, n := range widths {
			if n > w && n > minCol {
				w, at = n, c
			}
		}
		if at < 0 {
			break
		}
		widths[at]--
	}

	// A header of empty cells is a real markdown table — the pipes are there to
	// get two aligned columns, not to label them — so there is nothing to draw
	// in bold and no divider to draw under it.
	header := 0
	if mdAnyText(rows[0]) {
		header = 1
	}

	var out []string
	for i, row := range rows {
		cells := make([][]string, cols)
		height := 0
		for c := 0; c < cols; c++ {
			text := ""
			if c < len(row) {
				text = row[c]
			}
			if i < header {
				// One style over the whole cell, so the markers are stripped
				// first for the same reason a heading strips them.
				for _, l := range wrapWords(mdWords([]mdSpan{{text: mdStripInline(text)}}), widths[c], "", "") {
					cells[c] = append(cells[c], mdBoldStyle.Render(l))
				}
			} else {
				cells[c] = wrapWords(mdWords(parseInline(text)), widths[c], "", "")
			}
			height = max(height, len(cells[c]))
		}

		for line := 0; line < max(1, height); line++ {
			var b strings.Builder
			b.WriteString(prefix)
			for c := 0; c < cols; c++ {
				cell := ""
				if line < len(cells[c]) {
					cell = cells[c][line]
				}
				if c > 0 {
					b.WriteString(strings.Repeat(" ", gap))
				}
				b.WriteString(cell)
				// The last column is not padded: trailing spaces buy nothing
				// and the rule is never to touch the final column.
				if c < cols-1 {
					b.WriteString(strings.Repeat(" ", max(0, widths[c]-visibleLen(cell))))
				}
			}
			// Padding past the last filled cell buys nothing and pushes the
			// row towards a column it has no business touching.
			row := strings.TrimRight(b.String(), " ")
			// A table with more columns than the pane has room for cannot be
			// made to fit by shrinking alone, so the row is cut. That is the
			// one truncation here that is the rendering rather than a guard.
			out = append(out, truncateVisible(row, len(prefix)+maxWidth))
		}

		if i+1 == header {
			var b strings.Builder
			for c := 0; c < cols; c++ {
				if c > 0 {
					b.WriteString(strings.Repeat(" ", gap))
				}
				b.WriteString(strings.Repeat(mdRule, widths[c]))
			}
			out = append(out, prefix+mutedStyle.Render(b.String()))
		}
	}
	return out
}

func mdSum(n []int) int {
	t := 0
	for _, v := range n {
		t += v
	}
	return t
}

// mdAnyText reports whether any cell in a row has content.
func mdAnyText(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- indents

// mdIndent is how many columns a line is indented, counting a tab as four —
// which is what a markdown writer means by one level.
func mdIndent(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

// mdUnindent removes up to n columns of leading whitespace.
func mdUnindent(line string, n int) string {
	i, col := 0, 0
	for i < len(line) && col < n {
		switch line[i] {
		case ' ':
			col++
		case '\t':
			col += 4
		default:
			return line[i:]
		}
		i++
	}
	return line[i:]
}
