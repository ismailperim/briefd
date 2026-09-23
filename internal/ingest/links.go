package ingest

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Link is a reference from one document to another: an Obsidian-style
// wikilink or a relative Markdown link to a .md file. External URLs,
// images and same-document anchors are not links between documents.
type Link struct {
	// Target is the link as written: "Refund rules", "refund-rules#Window",
	// "../rules/refunds.md".
	Target string
	// Kind is "wikilink" or "markdown".
	Kind string
}

var (
	fencedCode = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~")
	inlineCode = regexp.MustCompile("`[^`\n]*`")
	// [[target]], [[target|alias]], [[target#heading]], but not ![[embeds]].
	wikiLink = regexp.MustCompile(`(^|[^!])\[\[([^\[\]|#]+)(?:#[^\[\]|]*)?(?:\|[^\[\]]*)?\]\]`)
	// [text](target) and [text](target "title"), but not ![images](…).
	mdLink = regexp.MustCompile(`(^|[^!])\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
)

// ExtractLinks lists the document links in a Markdown body, in order of
// first appearance, without duplicates. Code blocks are ignored.
func ExtractLinks(body []byte) []Link {
	text := inlineCode.ReplaceAllString(fencedCode.ReplaceAllString(string(body), ""), "")
	seen := map[string]bool{}
	var out []Link
	add := func(target, kind string) {
		key := kind + "\x00" + target
		if target == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Link{Target: target, Kind: kind})
	}
	for _, m := range wikiLink.FindAllStringSubmatch(text, -1) {
		add(strings.TrimSpace(m[2]), "wikilink")
	}
	for _, m := range mdLink.FindAllStringSubmatch(text, -1) {
		t := strings.TrimSpace(m[2])
		if i := strings.IndexByte(t, '#'); i >= 0 {
			t = t[:i]
		}
		t = strings.TrimSpace(t)
		if t == "" || strings.Contains(t, "://") || strings.HasPrefix(t, "mailto:") || strings.HasPrefix(t, "/") {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(t), ".md") {
			continue
		}
		add(t, "markdown")
	}
	return out
}

// Resolver maps link targets to repository-relative document paths the way
// Obsidian does: a Markdown link is relative to the linking file; a
// wikilink names a file by path or, without a slash, by its base name
// anywhere in the repository (shortest path wins when several match).
type Resolver struct {
	paths  map[string]bool
	byBase map[string][]string
}

// NewResolver indexes the known document paths.
func NewResolver(docPaths []string) *Resolver {
	r := &Resolver{paths: make(map[string]bool, len(docPaths)), byBase: map[string][]string{}}
	for _, p := range docPaths {
		r.paths[p] = true
		base := strings.ToLower(strings.TrimSuffix(path.Base(p), ".md"))
		r.byBase[base] = append(r.byBase[base], p)
	}
	for _, list := range r.byBase {
		sort.Slice(list, func(i, j int) bool {
			if len(list[i]) != len(list[j]) {
				return len(list[i]) < len(list[j])
			}
			return list[i] < list[j]
		})
	}
	return r
}

// Resolve returns the target document's path, or "" when the link is
// broken.
func (r *Resolver) Resolve(from string, l Link) string {
	t := strings.TrimSpace(l.Target)
	if l.Kind == "markdown" {
		p := path.Clean(path.Join(path.Dir(from), t))
		if r.paths[p] {
			return p
		}
		return ""
	}
	// Wikilink: an explicit path (with or without .md), tried as given,
	// relative to the repository root and to the linking file.
	if strings.Contains(t, "/") {
		for _, cand := range []string{t, t + ".md", path.Clean(path.Join(path.Dir(from), t)), path.Clean(path.Join(path.Dir(from), t)) + ".md"} {
			if r.paths[cand] {
				return cand
			}
		}
	}
	base := strings.ToLower(strings.TrimSuffix(path.Base(t), ".md"))
	if list := r.byBase[base]; len(list) > 0 {
		return list[0]
	}
	return ""
}
