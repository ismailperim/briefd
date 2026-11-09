package confluence

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// ToMarkdown converts a Confluence page body (rendered "view" HTML, or
// storage format as a fallback) to compact Markdown. It keeps what an agent
// can use — headings, paragraphs, lists, tables, code, links, emphasis —
// and drops styling, scripts and images, because every character is spent
// from the caller's token budget.
func ToMarkdown(src string) string {
	nodes, err := html.ParseFragment(strings.NewReader(src), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return strings.TrimSpace(src)
	}
	w := &mdWriter{}
	for _, n := range nodes {
		w.block(n)
	}
	return tidy(w.sb.String())
}

type mdWriter struct {
	sb strings.Builder
	// list holds one entry per open list: -1 for bullets, else the next
	// ordinal.
	list []int
}

var blankRuns = regexp.MustCompile(`\n{3,}`)

func tidy(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(blankRuns.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

func (w *mdWriter) para(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	w.sb.WriteString("\n\n" + s + "\n\n")
}

// block renders a node in block context.
func (w *mdWriter) block(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		w.para(collapse(n.Data))
		return
	case html.ElementNode:
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			w.block(c)
		}
		return
	}
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Noscript, atom.Img, atom.Svg, atom.Button, atom.Template:
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level := int(n.Data[1] - '0')
		w.para(strings.Repeat("#", level) + " " + w.inline(n))
	case atom.P:
		w.para(w.inline(n))
	case atom.Br:
		w.sb.WriteString("\n")
	case atom.Hr:
		w.para("---")
	case atom.Pre:
		w.para("```" + codeLang(n) + "\n" + strings.Trim(textOf(n), "\n") + "\n```")
	case atom.Blockquote:
		inner := &mdWriter{}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			inner.block(c)
		}
		lines := strings.Split(tidy(inner.sb.String()), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight("> "+l, " ")
		}
		w.para(strings.Join(lines, "\n"))
	case atom.Ul, atom.Ol:
		w.listBlock(n)
	case atom.Table:
		w.para(table(w, n))
	default:
		if hasBlockChild(n) {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				w.block(c)
			}
		} else {
			w.para(w.inline(n))
		}
	}
}

func (w *mdWriter) listBlock(n *html.Node) {
	start := -1
	if n.DataAtom == atom.Ol {
		start = 1
	}
	w.list = append(w.list, start)
	var sb strings.Builder
	indent := strings.Repeat("  ", len(w.list)-1)
	for li := n.FirstChild; li != nil; li = li.NextSibling {
		if li.Type != html.ElementNode || li.DataAtom != atom.Li {
			continue
		}
		marker := "- "
		if top := &w.list[len(w.list)-1]; *top > 0 {
			marker = strconv.Itoa(*top) + ". "
			*top++
		}
		var text strings.Builder
		var nested strings.Builder
		for c := li.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && (c.DataAtom == atom.Ul || c.DataAtom == atom.Ol) {
				sub := &mdWriter{list: w.list}
				sub.listBlock(c)
				nested.WriteString(strings.Trim(sub.sb.String(), "\n") + "\n")
				continue
			}
			text.WriteString(w.inlineNode(c))
		}
		sb.WriteString(indent + marker + tightPunct.ReplaceAllString(strings.TrimSpace(collapse(text.String())), "$1") + "\n")
		sb.WriteString(nested.String())
	}
	w.list = w.list[:len(w.list)-1]
	if len(w.list) == 0 {
		w.para(sb.String())
	} else {
		w.sb.WriteString(sb.String())
	}
}

// inline renders n's children as one line of Markdown.
func (w *mdWriter) inline(n *html.Node) string {
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(w.inlineNode(c))
	}
	return tightPunct.ReplaceAllString(strings.TrimSpace(collapse(sb.String())), "$1")
}

// tightPunct removes the space inline wrappers leave before punctuation.
var tightPunct = regexp.MustCompile(` ([.,;:!?)])`)

func (w *mdWriter) inlineNode(n *html.Node) string {
	switch n.Type {
	case html.TextNode:
		return collapse(n.Data)
	case html.ElementNode:
	default:
		return ""
	}
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Noscript, atom.Img, atom.Svg, atom.Button, atom.Template:
		return ""
	case atom.Br:
		return " "
	case atom.Strong, atom.B:
		return wrap("**", w.inline(n))
	case atom.Em, atom.I:
		return wrap("_", w.inline(n))
	case atom.Code:
		if t := strings.TrimSpace(textOf(n)); t != "" {
			return " `" + t + "` "
		}
		return ""
	case atom.A:
		text := w.inline(n)
		href := attr(n, "href")
		if text == "" || href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "javascript:") {
			return " " + text + " "
		}
		return " [" + text + "](" + href + ") "
	case atom.Ul, atom.Ol, atom.Table, atom.Pre:
		sub := &mdWriter{}
		sub.block(n)
		return " " + collapse(tidy(sub.sb.String())) + " "
	}
	return " " + w.inline(n) + " "
}

func table(w *mdWriter, n *html.Node) string {
	var rows [][]string
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if c.DataAtom == atom.Tr {
				var row []string
				for cell := c.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Type == html.ElementNode && (cell.DataAtom == atom.Td || cell.DataAtom == atom.Th) {
						row = append(row, strings.ReplaceAll(w.inline(cell), "|", `\|`))
					}
				}
				if len(row) > 0 {
					rows = append(rows, row)
				}
				continue
			}
			if c.DataAtom != atom.Table { // nested tables are flattened into cells
				walk(c)
			}
		}
	}
	walk(n)
	if len(rows) == 0 {
		return ""
	}
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	var sb strings.Builder
	for i, r := range rows {
		for len(r) < cols {
			r = append(r, "")
		}
		sb.WriteString("| " + strings.Join(r, " | ") + " |\n")
		if i == 0 {
			sb.WriteString("|" + strings.Repeat(" --- |", cols) + "\n")
		}
	}
	return sb.String()
}

func hasBlockChild(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.DataAtom {
		case atom.P, atom.Div, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Ul, atom.Ol,
			atom.Table, atom.Pre, atom.Blockquote, atom.Hr, atom.Section, atom.Article, atom.Header,
			atom.Footer, atom.Main, atom.Nav, atom.Aside, atom.Figure, atom.Dl:
			return true
		}
		if hasBlockChild(c) {
			return true
		}
	}
	return false
}

func codeLang(n *html.Node) string {
	for _, x := range []*html.Node{n, n.FirstChild} {
		if x == nil {
			continue
		}
		if l := attr(x, "data-syntaxhighlighter-params"); l != "" {
			for _, p := range strings.Split(l, ";") {
				if k, v, ok := strings.Cut(strings.TrimSpace(p), ":"); ok && strings.TrimSpace(k) == "brush" {
					return strings.TrimSpace(v)
				}
			}
		}
		for _, cls := range strings.Fields(attr(x, "class")) {
			if l, ok := strings.CutPrefix(cls, "language-"); ok {
				return l
			}
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			sb.WriteString(x.Data)
		}
		if x.Type == html.ElementNode && x.DataAtom == atom.Br {
			sb.WriteString("\n")
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func wrap(mark, s string) string {
	if s == "" {
		return ""
	}
	return " " + mark + s + mark + " "
}

var spaces = regexp.MustCompile(`[ \t\r\n\x{00a0}]+`)

func collapse(s string) string { return spaces.ReplaceAllString(s, " ") }
