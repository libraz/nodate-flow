// Package richtext converts Asana rich text (the html_notes / html_text
// subset) to Markdown.
//
// The input is read with the lenient x/net/html tokenizer into a small tree:
// unclosed elements close at EOF, stray end tags are ignored, and conversion
// never fails. Unknown elements are dropped and their children kept.
package richtext

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Resolver maps an Asana user gid to a display name.
type Resolver interface {
	UserName(gid string) (string, bool)
}

// ToMarkdown converts Asana rich text to Markdown. r may be nil, in which
// case every mention renders as "@unknown user".
func ToMarkdown(htmlSrc string, r Resolver) string {
	htmlSrc = strings.ReplaceAll(htmlSrc, "\r\n", "\n")
	htmlSrc = strings.ReplaceAll(htmlSrc, "\r", "\n")
	root := parse(htmlSrc)
	rd := &renderer{res: r}
	return normalize(joinBlocks(rd.blocks(root.children), "\n\n"))
}

type node struct {
	tag      string // empty for a text node
	text     string
	attrs    map[string]string
	children []*node
}

func (n *node) attr(k string) (string, bool) {
	v, ok := n.attrs[k]
	return v, ok
}

var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// impliedClose lists the open elements a start tag closes, and the elements
// that stop the search for them.
var impliedClose = map[string]struct{ closes, stops []string }{
	"li": {[]string{"li"}, []string{"ul", "ol"}},
	"td": {[]string{"td", "th"}, []string{"tr", "table"}},
	"th": {[]string{"td", "th"}, []string{"tr", "table"}},
	"tr": {[]string{"tr"}, []string{"table"}},
}

func parse(src string) *node {
	root := &node{tag: "#root"}
	stack := []*node{root}
	z := html.NewTokenizer(strings.NewReader(src))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return root
		}
		tok := z.Token()
		top := stack[len(stack)-1]
		switch tt {
		case html.TextToken:
			top.children = append(top.children, &node{text: tok.Data})
		case html.StartTagToken, html.SelfClosingTagToken:
			if ic, ok := impliedClose[tok.Data]; ok {
				for i := len(stack) - 1; i >= 1; i-- {
					if slices.Contains(ic.stops, stack[i].tag) {
						break
					}
					if slices.Contains(ic.closes, stack[i].tag) {
						stack = stack[:i]
						break
					}
				}
				top = stack[len(stack)-1]
			}
			n := &node{tag: tok.Data, attrs: map[string]string{}}
			for _, a := range tok.Attr {
				if _, dup := n.attrs[a.Key]; !dup {
					n.attrs[a.Key] = a.Val
				}
			}
			top.children = append(top.children, n)
			if tt == html.StartTagToken && !voidTags[tok.Data] {
				stack = append(stack, n)
			}
		case html.EndTagToken:
			for i := len(stack) - 1; i >= 1; i-- {
				if stack[i].tag == tok.Data {
					stack = stack[:i]
					break
				}
			}
		}
	}
}

func isBlock(tag string) bool {
	switch tag {
	case "ul", "ol", "blockquote", "pre", "h1", "h2", "hr", "table":
		return true
	}
	return false
}

func isInline(tag string) bool {
	switch tag {
	case "strong", "b", "em", "i", "s", "strike", "code", "a", "img":
		return true
	}
	return false
}

func textContent(n *node, sb *strings.Builder) {
	if n.tag == "" {
		sb.WriteString(n.text)
		return
	}
	for _, c := range n.children {
		textContent(c, sb)
	}
}

// normalize strips trailing line whitespace, caps blank runs at one empty
// line, and trims the whole string.
func normalize(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	s = strings.Join(lines, "\n")
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.Trim(s, " \t\n")
}

type block struct {
	s    string
	list bool
}

func joinBlocks(bs []block, sep string) string {
	parts := make([]string, len(bs))
	for i, b := range bs {
		parts[i] = b.s
	}
	return strings.Join(parts, sep)
}

type renderer struct {
	res  Resolver
	cell bool // inside a table cell: no line starts, single line
}

// collector gathers a node run into blocks: inline content accumulates into
// a paragraph that block elements flush.
type collector struct {
	r   *renderer
	out []block
	cur *writer
}

func (r *renderer) blocks(nodes []*node) []block {
	c := &collector{r: r, cur: r.newWriter(true)}
	c.walk(nodes)
	c.flush()
	return c.out
}

func (c *collector) walk(nodes []*node) {
	for _, n := range nodes {
		switch {
		case n.tag == "":
			c.cur.text(n.text)
		case isBlock(n.tag):
			c.flush()
			if b := c.r.block(n); b.s != "" {
				c.out = append(c.out, b)
			}
		case isInline(n.tag):
			c.cur.node(n)
		default:
			c.walk(n.children)
		}
	}
}

func (c *collector) flush() {
	if s := normalize(c.cur.sb.String()); s != "" {
		c.out = append(c.out, block{s: s})
	}
	c.cur = c.r.newWriter(true)
}

func (r *renderer) block(n *node) block {
	switch n.tag {
	case "h1":
		return block{s: r.heading("# ", n)}
	case "h2":
		return block{s: r.heading("## ", n)}
	case "hr":
		return block{s: "---"}
	case "pre":
		return block{s: pre(n)}
	case "blockquote":
		return block{s: r.quote(n)}
	case "ul":
		return r.list(n, false)
	case "ol":
		return r.list(n, true)
	case "table":
		return block{s: r.table(n)}
	}
	return block{}
}

func (r *renderer) heading(prefix string, n *node) string {
	w := r.newWriter(false)
	for _, c := range n.children {
		w.node(c)
	}
	s := strings.Trim(strings.ReplaceAll(w.sb.String(), "\n", " "), " \t")
	if s == "" {
		return ""
	}
	return prefix + s
}

func pre(n *node) string {
	var sb strings.Builder
	textContent(n, &sb)
	body := strings.TrimSuffix(sb.String(), "\n")
	fence := "```"
	if strings.Contains(body, fence) {
		fence = "````"
	}
	if body == "" {
		return fence + "\n" + fence
	}
	return fence + "\n" + body + "\n" + fence
}

func (r *renderer) quote(n *node) string {
	bs := r.blocks(n.children)
	if len(bs) == 0 {
		return ""
	}
	lines := strings.Split(joinBlocks(bs, "\n\n"), "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + l
		}
	}
	return strings.Join(lines, "\n")
}

type listItem struct {
	nodes []*node
	real  bool // an li element, as opposed to stray content
}

func (r *renderer) list(n *node, ordered bool) block {
	var items []listItem
	var pend []*node
	flushPend := func() {
		if len(pend) > 0 {
			items = append(items, listItem{nodes: pend})
			pend = nil
		}
	}
	var collect func(ns []*node)
	collect = func(ns []*node) {
		for _, c := range ns {
			switch {
			case c.tag == "li":
				flushPend()
				items = append(items, listItem{nodes: c.children, real: true})
			case c.tag != "" && !isBlock(c.tag) && !isInline(c.tag):
				collect(c.children)
			default:
				pend = append(pend, c)
			}
		}
	}
	collect(n.children)
	flushPend()

	var out []string
	for _, it := range items {
		bs := r.blocks(it.nodes)
		if len(bs) == 0 && !it.real {
			continue
		}
		marker := "- "
		if ordered {
			marker = strconv.Itoa(len(out)+1) + ". "
		}
		pad := strings.Repeat(" ", len(marker))
		var sb strings.Builder
		for i, b := range bs {
			switch {
			case i == 0:
			case b.list:
				sb.WriteString("\n")
			default:
				sb.WriteString("\n\n")
			}
			sb.WriteString(b.s)
		}
		lines := strings.Split(sb.String(), "\n")
		for i, l := range lines {
			switch {
			case i == 0:
				lines[i] = strings.TrimRight(marker+l, " ")
			case l != "":
				lines[i] = pad + l
			}
		}
		out = append(out, strings.Join(lines, "\n"))
	}
	return block{s: strings.Join(out, "\n"), list: true}
}

func (r *renderer) table(n *node) string {
	cr := &renderer{res: r.res, cell: true}
	var rows [][]string
	var findRows func(ns []*node)
	findRows = func(ns []*node) {
		for _, c := range ns {
			switch c.tag {
			case "", "table":
			case "tr":
				rows = append(rows, cr.cells(c.children))
			default:
				findRows(c.children)
			}
		}
	}
	findRows(n.children)
	cols := 0
	for _, row := range rows {
		cols = max(cols, len(row))
	}
	if cols == 0 {
		return ""
	}
	line := func(cells []string) string {
		padded := make([]string, cols)
		copy(padded, cells)
		return "| " + strings.Join(padded, " | ") + " |"
	}
	sep := make([]string, cols)
	for i := range sep {
		sep[i] = "---"
	}
	lines := []string{line(rows[0]), line(sep)}
	for _, row := range rows[1:] {
		lines = append(lines, line(row))
	}
	return strings.Join(lines, "\n")
}

func (r *renderer) cells(ns []*node) []string {
	var out []string
	for _, c := range ns {
		switch c.tag {
		case "":
		case "td", "th":
			s := strings.ReplaceAll(joinBlocks(r.blocks(c.children), " "), "\n", " ")
			out = append(out, strings.ReplaceAll(strings.Trim(s, " \t"), "|", `\|`))
		default:
			out = append(out, r.cells(c.children)...)
		}
	}
	return out
}

// writer builds one inline run and tracks whether the next character starts
// an output line, which decides the line-start escapes.
type writer struct {
	r     *renderer
	sb    strings.Builder
	start bool
}

func (r *renderer) newWriter(start bool) *writer {
	return &writer{r: r, start: start && !r.cell}
}

func (w *writer) child(start bool) *writer {
	return w.r.newWriter(start)
}

const alwaysEscaped = "\\*_`~[]<>"

func blankInline(s string) bool { return strings.Trim(s, " \t") == "" }

// raw appends already-formed Markdown and updates the line-start state.
func (w *writer) raw(s string) {
	if s == "" {
		return
	}
	w.sb.WriteString(s)
	if w.r.cell {
		return
	}
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		w.start = blankInline(s[i+1:])
	} else if !blankInline(s) {
		w.start = false
	}
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// text appends a text node with Markdown escaping.
func (w *writer) text(s string) {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\n':
			w.sb.WriteByte('\n')
			w.start = !w.r.cell
		case r == ' ' || r == '\t':
			w.sb.WriteRune(r)
		case strings.ContainsRune(alwaysEscaped, r):
			w.sb.WriteByte('\\')
			w.sb.WriteRune(r)
			w.start = false
		case w.start && strings.ContainsRune("#-+=", r):
			w.sb.WriteByte('\\')
			w.sb.WriteRune(r)
			w.start = false
		case w.start && size == 1 && isDigit(s[i]):
			j := i
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			w.sb.WriteString(s[i:j])
			if j < len(s) && (s[j] == '.' || s[j] == ')') {
				w.sb.WriteByte('\\')
				w.sb.WriteByte(s[j])
				j++
			}
			w.start = false
			i = j
			continue
		default:
			w.sb.WriteRune(r)
			w.start = false
		}
		i += size
	}
}

func (w *writer) node(n *node) {
	switch n.tag {
	case "":
		w.text(n.text)
	case "strong", "b":
		w.wrap("**", n)
	case "em", "i":
		w.wrap("*", n)
	case "s", "strike":
		w.wrap("~~", n)
	case "code":
		var sb strings.Builder
		textContent(n, &sb)
		switch c := sb.String(); {
		case c == "":
		case strings.Contains(c, "`"):
			w.raw("`` " + c + " ``")
		default:
			w.raw("`" + c + "`")
		}
	case "a":
		w.anchor(n)
	case "img":
		if _, ok := n.attr("data-asana-gid"); ok {
			return
		}
		alt, _ := n.attr("alt")
		src, _ := n.attr("src")
		w.raw("![" + alt + "](" + src + ")")
	default:
		if isBlock(n.tag) {
			if b := w.r.block(n); b.s != "" {
				w.raw("\n\n" + b.s + "\n\n")
			}
			return
		}
		for _, c := range n.children {
			w.node(c)
		}
	}
}

// wrap surrounds the children with marker, keeping edge whitespace outside
// so the emphasis stays valid; empty content emits nothing.
func (w *writer) wrap(marker string, n *node) {
	sub := w.child(false)
	for _, c := range n.children {
		sub.node(c)
	}
	s := sub.sb.String()
	core := strings.Trim(s, " \t\n")
	if core == "" {
		w.raw(s)
		return
	}
	lead := s[:len(s)-len(strings.TrimLeft(s, " \t\n"))]
	w.raw(lead)
	w.raw(marker + core + marker)
	w.raw(s[len(lead)+len(core):])
}

func (w *writer) anchor(n *node) {
	if typ, _ := n.attr("data-asana-type"); typ == "user" {
		gid, _ := n.attr("data-asana-gid")
		name := "unknown user"
		if w.r.res != nil {
			if v, ok := w.r.res.UserName(gid); ok && v != "" {
				name = v
			}
		}
		w.raw("@" + name)
		return
	}
	href, _ := n.attr("href")
	if href == "" {
		for _, c := range n.children {
			w.node(c)
		}
		return
	}
	sub := w.child(false)
	for _, c := range n.children {
		sub.node(c)
	}
	text := sub.sb.String()
	if strings.TrimSpace(text) == "" {
		text = href
	}
	w.raw("[" + text + "](" + href + ")")
}
