package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/ismailperim/briefd/internal/tokenizer"
)

// Chunking parameters from SPEC §4.3.
const (
	// TargetChunkTokens is the upper end of the preferred chunk size. Sections
	// larger than MaxChunkTokens are split into parts of roughly this size.
	TargetChunkTokens = 500
	// MaxChunkTokens is the hard ceiling for a single chunk.
	MaxChunkTokens = 800
	// HeadingSeparator joins the heading breadcrumb into a single string.
	HeadingSeparator = " > "
)

// Chunk is a retrievable unit of a document: a heading-bounded section, or a
// part of one when the section exceeds MaxChunkTokens.
type Chunk struct {
	// ID is stable across re-indexing as long as the document path and
	// heading breadcrumb do not change: hash(doc_path, heading_path, part).
	ID string
	// HeadingPath is the breadcrumb "Title > H2 > H3".
	HeadingPath string
	// Content is the raw Markdown of the section, including its heading line.
	Content string
	// Tokens is the estimated token count of Content.
	Tokens int
	// ContentHash is a SHA-256 of Content, used to skip unchanged embeddings.
	ContentHash string
	// Position is the 0-based order of the chunk within the document.
	Position int
}

// section is a heading-bounded slice of the source before size splitting.
type section struct {
	path  []string
	start int
	end   int
}

var markdown = goldmark.New()

// ChunkDocument splits a Markdown body into chunks. title is used as the root
// of every heading breadcrumb; when empty, the first H1 is used, and when
// there is no H1 either, docPath is used.
func ChunkDocument(docPath, title string, body []byte) (resolvedTitle string, chunks []Chunk) {
	doc := markdown.Parser().Parse(text.NewReader(body))

	type heading struct {
		level     int
		text      string
		lineStart int // byte offset of the first character of the heading's line
		blockEnd  int // byte offset just past the heading block
	}
	var headings []heading
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		h, ok := n.(*ast.Heading)
		if !ok || h.Lines().Len() == 0 {
			continue
		}
		first := h.Lines().At(0)
		last := h.Lines().At(h.Lines().Len() - 1)
		headings = append(headings, heading{
			level:     h.Level,
			text:      headingText(body, h),
			lineStart: lineStartBefore(body, first.Start),
			blockEnd:  lineEndAfter(body, last.Stop),
		})
	}

	// Resolve the title: front matter > first H1 > path.
	firstH1 := -1
	for i, h := range headings {
		if h.level == 1 {
			firstH1 = i
			break
		}
	}
	resolvedTitle = strings.TrimSpace(title)
	if resolvedTitle == "" && firstH1 >= 0 {
		resolvedTitle = headings[firstH1].text
	}
	if resolvedTitle == "" {
		resolvedTitle = docPath
	}

	// Build sections. H1 is a boundary that resets the breadcrumb; H2 and H3
	// open new sections; deeper headings stay inside the enclosing section.
	var sections []section
	current := section{path: []string{resolvedTitle}, start: 0}
	var h2 string
	for i, h := range headings {
		if h.level > 3 {
			continue
		}
		current.end = h.lineStart
		sections = append(sections, current)

		start := h.lineStart
		switch h.level {
		case 1:
			h2 = ""
			// The document's H1 duplicates the title; it is not content.
			if i == firstH1 {
				start = h.blockEnd
			}
			current = section{path: []string{resolvedTitle}, start: start}
		case 2:
			h2 = h.text
			current = section{path: []string{resolvedTitle, h.text}, start: start}
		case 3:
			path := []string{resolvedTitle}
			if h2 != "" {
				path = append(path, h2)
			}
			current = section{path: append(path, h.text), start: start}
		}
	}
	current.end = len(body)
	sections = append(sections, current)

	// Emit chunks, splitting oversized sections on paragraph boundaries.
	seen := map[string]int{}
	for _, s := range sections {
		content := strings.TrimSpace(string(body[s.start:s.end]))
		if content == "" {
			continue
		}
		headingPath := strings.Join(s.path, HeadingSeparator)
		// Disambiguate repeated breadcrumbs so IDs stay unique.
		key := headingPath
		seen[key]++
		if n := seen[key]; n > 1 {
			headingPath = fmt.Sprintf("%s (%d)", headingPath, n)
		}
		parts := splitOversized(content)
		for p, part := range parts {
			chunks = append(chunks, Chunk{
				ID:          chunkID(docPath, headingPath, p),
				HeadingPath: headingPath,
				Content:     part,
				Tokens:      tokenizer.Count(part),
				ContentHash: hashHex([]byte(part)),
				Position:    len(chunks),
			})
		}
	}
	return resolvedTitle, chunks
}

// splitOversized returns content unchanged when it fits in MaxChunkTokens;
// otherwise it packs paragraphs greedily into parts of at most
// TargetChunkTokens. A single paragraph that is itself larger than
// MaxChunkTokens is split on line boundaries, then on rune boundaries as a
// last resort, so no part ever exceeds MaxChunkTokens.
func splitOversized(content string) []string {
	if tokenizer.Count(content) <= MaxChunkTokens {
		return []string{content}
	}
	var parts []string
	var buf strings.Builder
	flush := func() {
		if s := strings.TrimSpace(buf.String()); s != "" {
			parts = append(parts, s)
		}
		buf.Reset()
	}
	for _, unit := range splitUnits(content, "\n\n", MaxChunkTokens) {
		if buf.Len() > 0 && tokenizer.Count(buf.String())+tokenizer.Count(unit) > TargetChunkTokens {
			flush()
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(unit)
	}
	flush()
	return parts
}

// splitUnits splits s on sep and recursively breaks any unit larger than
// limit tokens, first on single newlines and finally on rune boundaries.
func splitUnits(s, sep string, limit int) []string {
	var out []string
	for _, unit := range strings.Split(s, sep) {
		unit = strings.TrimSpace(unit)
		if unit == "" {
			continue
		}
		if tokenizer.Count(unit) <= limit {
			out = append(out, unit)
			continue
		}
		if sep == "\n\n" {
			out = append(out, splitUnits(unit, "\n", limit)...)
			continue
		}
		out = append(out, splitByRunes(unit, limit)...)
	}
	return out
}

func splitByRunes(s string, limit int) []string {
	var out []string
	runes := []rune(s)
	for len(runes) > 0 {
		n := len(runes)
		for n > 1 && tokenizer.Count(string(runes[:n])) > limit {
			n = n * 3 / 4
		}
		out = append(out, strings.TrimSpace(string(runes[:n])))
		runes = runes[n:]
	}
	return out
}

func headingText(src []byte, h *ast.Heading) string {
	var sb strings.Builder
	lines := h.Lines()
	for i := range lines.Len() {
		seg := lines.At(i)
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.Write(bytes.TrimSpace(seg.Value(src)))
	}
	return strings.TrimSpace(sb.String())
}

// lineStartBefore returns the offset of the first byte of the line
// containing pos.
func lineStartBefore(src []byte, pos int) int {
	if pos > len(src) {
		pos = len(src)
	}
	if i := bytes.LastIndexByte(src[:pos], '\n'); i >= 0 {
		return i + 1
	}
	return 0
}

// lineEndAfter returns the offset just past the newline that ends the line
// containing pos (or len(src)).
func lineEndAfter(src []byte, pos int) int {
	if pos >= len(src) {
		return len(src)
	}
	if i := bytes.IndexByte(src[pos:], '\n'); i >= 0 {
		return pos + i + 1
	}
	return len(src)
}

func chunkID(docPath, headingPath string, part int) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%d", docPath, headingPath, part)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
