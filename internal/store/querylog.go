package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// QueryRecord is one logged retrieval request.
type QueryRecord struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Surface  string    `json:"surface"`
	Name     string    `json:"name"`
	Query    string    `json:"query"`
	Scopes   []string  `json:"scopes"`
	Mode     string    `json:"mode"`
	Results  int       `json:"results"`
	TopScore float64   `json:"top_score"`
	Margin   float64   `json:"margin"`
	Tokens   int       `json:"tokens"`
	BundleID string    `json:"bundle_id,omitempty"`
	Client   string    `json:"client,omitempty"`
}

// LogQuery appends a record to the query log.
func (s *Store) LogQuery(ctx context.Context, r QueryRecord) error {
	at := r.At
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO query_log (at, surface, name, query, scopes, mode, results, top_score, margin, tokens, bundle_id, client)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		at.UTC().Format(time.RFC3339Nano), r.Surface, r.Name, r.Query, strings.Join(r.Scopes, ","), r.Mode,
		r.Results, r.TopScore, r.Margin, r.Tokens, r.BundleID, r.Client)
	if err != nil {
		return fmt.Errorf("logging query: %w", err)
	}
	return nil
}

// PruneQueryLog deletes records older than retention and returns the count.
func (s *Store) PruneQueryLog(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().Add(-retention).UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `DELETE FROM query_log WHERE at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("pruning query log: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Gap is a logged question that the knowledge base did not answer well,
// aggregated over identical query texts.
type Gap struct {
	Query  string   `json:"query"`
	Scopes []string `json:"scopes"`
	// Name is the tool or route that asked (search_context, compile_bundle, …).
	Name string `json:"name"`
	// At is the most recent time the question was asked.
	At       time.Time `json:"at"`
	Results  int       `json:"results"`
	TopScore float64   `json:"top_score"`
	Margin   float64   `json:"margin"`
	// Reason is "no_useful_sections" (the agent reported none of the bundle
	// helped), "no_results" (nothing matched), or "low_confidence".
	Reason string `json:"reason"`
	// Times counts how often the same query text was logged.
	Times int `json:"times"`
}

// Gaps returns recent questions that produced no useful section — by the
// agent's own feedback or because nothing matched — grouped by query text,
// most frequent first; then the lowest-confidence recent queries.
func (s *Store) Gaps(ctx context.Context, since time.Time, limit int) (noUseful, lowConfidence []Gap, err error) {
	cutoff := since.UTC().Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx, `
		SELECT q.query, q.scopes, q.name, MAX(q.at), COUNT(*),
		       MAX(q.results), MAX(q.top_score), MAX(q.margin),
		       CASE WHEN MAX(q.results) = 0 THEN 'no_results' ELSE 'no_useful_sections' END
		FROM query_log q
		WHERE q.at >= ? AND (
			q.results = 0
			OR (q.bundle_id != '' AND EXISTS (SELECT 1 FROM usage_events u WHERE u.bundle_id = q.bundle_id)
			    AND NOT EXISTS (SELECT 1 FROM usage_events u WHERE u.bundle_id = q.bundle_id AND u.useful = 1))
		)
		GROUP BY q.query, q.scopes, q.name
		ORDER BY COUNT(*) DESC, MAX(q.at) DESC
		LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("listing gaps: %w", err)
	}
	noUseful, err = scanGaps(rows)
	if err != nil {
		return nil, nil, err
	}
	rows, err = s.db.QueryContext(ctx, `
		SELECT q.query, q.scopes, q.name, MAX(q.at), COUNT(*),
		       MAX(q.results), MAX(q.top_score), MAX(q.margin), 'low_confidence'
		FROM query_log q
		WHERE q.at >= ? AND q.results > 0 AND q.top_score > 0
		GROUP BY q.query, q.scopes, q.name
		ORDER BY MAX(q.margin) ASC, MAX(q.at) DESC
		LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("listing low-confidence queries: %w", err)
	}
	lowConfidence, err = scanGaps(rows)
	return noUseful, lowConfidence, err
}

func scanGaps(rows *sql.Rows) ([]Gap, error) {
	defer rows.Close()
	var out []Gap
	for rows.Next() {
		var g Gap
		var scopes, at string
		if err := rows.Scan(&g.Query, &scopes, &g.Name, &at, &g.Times, &g.Results, &g.TopScore, &g.Margin, &g.Reason); err != nil {
			return nil, fmt.Errorf("reading gaps: %w", err)
		}
		g.At, _ = time.Parse(time.RFC3339Nano, at)
		if scopes != "" {
			g.Scopes = strings.Split(scopes, ",")
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// QueryLogStats returns the number of records and the oldest timestamp.
func (s *Store) QueryLogStats(ctx context.Context) (count int, oldest time.Time, err error) {
	var at *string
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(at) FROM query_log`).Scan(&count, &at); err != nil {
		return 0, time.Time{}, fmt.Errorf("query log stats: %w", err)
	}
	if at != nil {
		oldest, _ = time.Parse(time.RFC3339Nano, *at)
	}
	return count, oldest, nil
}
