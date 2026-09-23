// Package ingest turns a knowledge repository of Markdown files into
// documents and chunks (SPEC §2, §4).
package ingest

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Document is a parsed knowledge file ready to be stored.
type Document struct {
	// Path is the repository-relative path with forward slashes.
	Path  string
	Scope string
	Title string
	Tags  []string
	Refs  []string
	// FrontMatter is the raw YAML header (without delimiters), or "".
	FrontMatter string
	// ContentHash is a SHA-256 of the file as read from disk.
	ContentHash string
	Chunks      []Chunk
	// Links are the document's wikilinks and relative Markdown links, as
	// written; the indexer resolves them against the whole repository.
	Links []Link
}

// Parse builds a Document from file contents. relPath must already be
// repository-relative with forward slashes.
func Parse(relPath string, src []byte) (*Document, error) {
	scope, ok := ScopeOf(relPath)
	if !ok {
		return nil, fmt.Errorf("parsing %s: path is outside domain/, conventions/ or projects/<name>/", relPath)
	}
	fm, raw, body, err := SplitFrontMatter(src)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", relPath, err)
	}
	title, chunks := ChunkDocument(relPath, fm.Title, body)
	return &Document{
		Path:        relPath,
		Scope:       scope,
		Title:       title,
		Tags:        fm.Tags,
		Refs:        fm.Refs,
		FrontMatter: raw,
		ContentHash: hashHex(src),
		Chunks:      chunks,
		Links:       ExtractLinks(body),
	}, nil
}

// ScopeOf derives the visibility scope from a repository-relative path
// (SPEC §2): "domain", "conventions", or "projects/<name>". Files outside
// those folders are not knowledge and return ok == false.
func ScopeOf(relPath string) (scope string, ok bool) {
	parts := strings.Split(path.Clean(relPath), "/")
	switch {
	case len(parts) >= 2 && (parts[0] == "domain" || parts[0] == "conventions"):
		return parts[0], true
	case len(parts) >= 3 && parts[0] == "projects":
		return "projects/" + parts[1], true
	}
	return "", false
}

// File is a Markdown file discovered under a knowledge repository root.
type File struct {
	RelPath string // forward-slash, repository-relative
	AbsPath string
}

// Walk lists every knowledge Markdown file under root, in sorted order.
// Hidden directories (".git", ".github", ...) and files outside the scope
// folders are skipped.
func Walk(root string) ([]File, error) {
	var files []File
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || !strings.EqualFold(filepath.Ext(name), ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if _, ok := ScopeOf(rel); !ok {
			return nil
		}
		files = append(files, File{RelPath: rel, AbsPath: p})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].RelPath < files[j].RelPath })
	return files, nil
}

// Load reads and parses a discovered file.
func Load(f File) (*Document, error) {
	src, err := os.ReadFile(f.AbsPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", f.RelPath, err)
	}
	return Parse(f.RelPath, src)
}
