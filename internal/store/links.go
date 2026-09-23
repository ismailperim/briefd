package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ismailperim/briefd/internal/ingest"
)

// LinkRef is one end of a resolved link between documents.
type LinkRef struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	Scope string `json:"scope"`
}

// BrokenLink is a link whose target no document matches.
type BrokenLink struct {
	From   string `json:"from"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

// GraphNode is a document in the link graph.
type GraphNode struct {
	Path      string    `json:"path"`
	Title     string    `json:"title"`
	Scope     string    `json:"scope"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	In        int       `json:"in"`
	Out       int       `json:"out"`
}

// GraphEdge is a resolved link.
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Graph is the document link graph plus its maintenance signals.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
	Links int         `json:"links"`
	// Orphans are documents nothing links to (excluding self links).
	Orphans []LinkRef    `json:"orphans"`
	Broken  []BrokenLink `json:"broken"`
}

// replaceLinks stores a document's links as written, unresolved. Called
// inside UpsertDocument's transaction; ResolveLinks fills to_path.
func replaceLinks(ctx context.Context, tx *sql.Tx, from string, links []ingest.Link) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM doc_links WHERE from_path = ?`, from); err != nil {
		return fmt.Errorf("clearing links of %s: %w", from, err)
	}
	for _, l := range links {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO doc_links (from_path, target, kind) VALUES (?, ?, ?)`, from, l.Target, l.Kind); err != nil {
			return fmt.Errorf("storing link %s -> %s: %w", from, l.Target, err)
		}
	}
	return nil
}

// ResolveLinks recomputes every link's target path against the current
// set of documents. Run after documents were added, changed or deleted.
func (s *Store) ResolveLinks(ctx context.Context) error {
	paths, err := s.DocumentPaths(ctx)
	if err != nil {
		return err
	}
	r := ingest.NewResolver(paths)
	rows, err := s.db.QueryContext(ctx, `SELECT from_path, target, kind, to_path FROM doc_links`)
	if err != nil {
		return fmt.Errorf("listing links: %w", err)
	}
	type upd struct{ from, target, kind, to string }
	var updates []upd
	for rows.Next() {
		var from, target, kind, to string
		if err := rows.Scan(&from, &target, &kind, &to); err != nil {
			rows.Close()
			return fmt.Errorf("listing links: %w", err)
		}
		if resolved := r.Resolve(from, ingest.Link{Target: target, Kind: kind}); resolved != to {
			updates = append(updates, upd{from, target, kind, resolved})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("listing links: %w", err)
	}
	if len(updates) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("resolving links: %w", err)
	}
	defer tx.Rollback() // no-op after commit
	for _, u := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE doc_links SET to_path = ? WHERE from_path = ? AND target = ? AND kind = ?`, u.to, u.from, u.target, u.kind); err != nil {
			return fmt.Errorf("resolving link %s -> %s: %w", u.from, u.target, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("resolving links: %w", err)
	}
	return nil
}

// DocumentPaths lists every indexed document path.
func (s *Store) DocumentPaths(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM documents ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("listing documents: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("listing documents: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LinksVersion identifies the link-extraction rules. Documents stored with
// an older value are re-parsed on the next index run even when their
// content is unchanged. Bump it whenever ingest.ExtractLinks or Parse start
// finding links they did not find before.
//
//	1 — wikilinks and relative Markdown links in the body
//	2 — also wikilinks in front-matter properties (related:, up:, …)
const LinksVersion = 2

// DocumentsWithoutLinkScan lists documents whose links were extracted by
// older rules (or not at all).
func (s *Store) DocumentsWithoutLinkScan(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM documents WHERE links_scanned < ?`, LinksVersion)
	if err != nil {
		return nil, fmt.Errorf("listing unscanned documents: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("listing unscanned documents: %w", err)
		}
		out[p] = true
	}
	return out, rows.Err()
}

// LinksOf returns a document's outgoing resolved links and its backlinks.
func (s *Store) LinksOf(ctx context.Context, docPath string) (out, back []LinkRef, err error) {
	q := func(sqlText string) ([]LinkRef, error) {
		rows, err := s.db.QueryContext(ctx, sqlText, docPath)
		if err != nil {
			return nil, fmt.Errorf("listing links of %s: %w", docPath, err)
		}
		defer rows.Close()
		var refs []LinkRef
		for rows.Next() {
			var r LinkRef
			if err := rows.Scan(&r.Path, &r.Title, &r.Scope); err != nil {
				return nil, fmt.Errorf("listing links of %s: %w", docPath, err)
			}
			refs = append(refs, r)
		}
		return refs, rows.Err()
	}
	if out, err = q(`SELECT DISTINCT d.path, d.title, d.scope FROM doc_links l JOIN documents d ON d.path = l.to_path WHERE l.from_path = ? AND l.to_path != '' ORDER BY d.path`); err != nil {
		return nil, nil, err
	}
	back, err = q(`SELECT DISTINCT d.path, d.title, d.scope FROM doc_links l JOIN documents d ON d.path = l.from_path WHERE l.to_path = ? ORDER BY d.path`)
	return out, back, err
}

// LinkGraph returns every document as a node, resolved links as edges,
// and the orphans and broken links.
func (s *Store) LinkGraph(ctx context.Context) (*Graph, error) {
	g := &Graph{Nodes: []GraphNode{}, Edges: []GraphEdge{}, Orphans: []LinkRef{}, Broken: []BrokenLink{}}
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.path, d.title, d.scope, d.updated_at,
		       (SELECT COUNT(DISTINCT from_path) FROM doc_links WHERE to_path = d.path AND from_path != d.path),
		       (SELECT COUNT(DISTINCT to_path) FROM doc_links WHERE from_path = d.path AND to_path != '' AND to_path != d.path)
		FROM documents d ORDER BY d.path`)
	if err != nil {
		return nil, fmt.Errorf("building link graph: %w", err)
	}
	for rows.Next() {
		var n GraphNode
		var updated string
		if err := rows.Scan(&n.Path, &n.Title, &n.Scope, &updated, &n.In, &n.Out); err != nil {
			rows.Close()
			return nil, fmt.Errorf("building link graph: %w", err)
		}
		n.UpdatedAt = parseTime(updated)
		g.Nodes = append(g.Nodes, n)
		if n.In == 0 {
			g.Orphans = append(g.Orphans, LinkRef{Path: n.Path, Title: n.Title, Scope: n.Scope})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("building link graph: %w", err)
	}
	rows, err = s.db.QueryContext(ctx, `SELECT DISTINCT from_path, to_path FROM doc_links WHERE to_path != '' AND to_path != from_path ORDER BY from_path, to_path`)
	if err != nil {
		return nil, fmt.Errorf("building link graph: %w", err)
	}
	for rows.Next() {
		var e GraphEdge
		if err := rows.Scan(&e.From, &e.To); err != nil {
			rows.Close()
			return nil, fmt.Errorf("building link graph: %w", err)
		}
		g.Edges = append(g.Edges, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("building link graph: %w", err)
	}
	g.Links = len(g.Edges)
	rows, err = s.db.QueryContext(ctx, `SELECT from_path, target, kind FROM doc_links WHERE to_path = '' ORDER BY from_path, target`)
	if err != nil {
		return nil, fmt.Errorf("building link graph: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var b BrokenLink
		if err := rows.Scan(&b.From, &b.Target, &b.Kind); err != nil {
			return nil, fmt.Errorf("building link graph: %w", err)
		}
		g.Broken = append(g.Broken, b)
	}
	return g, rows.Err()
}
