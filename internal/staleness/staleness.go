// Package staleness detects knowledge that lags the code it governs: a
// document's `refs` globs name code paths, and commits that touched those
// paths after the document last changed are counted as drift (ADR-0007).
package staleness

import (
	"regexp"
	"strings"
	"time"

	"github.com/ismailperim/briefd/internal/gitsync"
	"github.com/ismailperim/briefd/internal/store"
)

// Compute returns a drift row for every document whose refs match a change
// newer than the document itself. Documents with unknown age are skipped:
// without a date there is nothing to compare against.
func Compute(docs []store.DocRefs, repo string, changes []gitsync.Change, now time.Time) []store.Drift {
	var out []store.Drift
	for _, doc := range docs {
		if doc.UpdatedAt.IsZero() {
			continue
		}
		matchers := compile(doc.Refs)
		if len(matchers) == 0 {
			continue
		}
		d := store.Drift{DocPath: doc.Path, Repo: repo, CheckedAt: now}
		for _, ch := range changes {
			if !ch.When.After(doc.UpdatedAt) {
				continue
			}
			if p := firstMatch(matchers, ch.Paths); p != "" {
				d.Commits++
				if ch.When.After(d.LastChangeAt) {
					d.LastChangeAt, d.LastPath = ch.When, p
				}
			}
		}
		if d.Commits > 0 {
			out = append(out, d)
		}
	}
	return out
}

// Since returns the earliest document age, the point from which history
// must be walked; zero when no document has a known age.
func Since(docs []store.DocRefs) time.Time {
	var min time.Time
	for _, d := range docs {
		if !d.UpdatedAt.IsZero() && (min.IsZero() || d.UpdatedAt.Before(min)) {
			min = d.UpdatedAt
		}
	}
	return min
}

func firstMatch(matchers []*regexp.Regexp, paths []string) string {
	for _, p := range paths {
		for _, m := range matchers {
			if m.MatchString(p) {
				return p
			}
		}
	}
	return ""
}

func compile(globs []string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, g := range globs {
		if re, err := regexp.Compile(globToRegexp(g)); err == nil {
			out = append(out, re)
		}
	}
	return out
}

// Match reports whether a repository-relative path matches a glob. `*` and
// `?` stay within one path segment; `**` spans segments, and a leading
// `**/` also matches the root. A pattern that names a directory (no
// wildcard, or ending in `/`) matches everything beneath it.
func Match(glob, path string) bool {
	re, err := regexp.Compile(globToRegexp(glob))
	return err == nil && re.MatchString(path)
}

func globToRegexp(glob string) string {
	glob = strings.TrimPrefix(strings.TrimSpace(glob), "./")
	glob = strings.TrimPrefix(glob, "/")
	if glob == "" {
		return `^$`
	}
	if !strings.ContainsAny(glob, "*?") {
		// A plain file or directory prefix.
		return `^` + regexp.QuoteMeta(strings.TrimSuffix(glob, "/")) + `(/.*)?$`
	}
	var sb strings.Builder
	sb.WriteString(`^`)
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; {
		case c == '*' && i+1 < len(glob) && glob[i+1] == '*':
			i++
			if i+1 < len(glob) && glob[i+1] == '/' {
				i++
				sb.WriteString(`(.*/)?`) // "**/" also matches nothing
			} else {
				sb.WriteString(`.*`)
			}
		case c == '*':
			sb.WriteString(`[^/]*`)
		case c == '?':
			sb.WriteString(`[^/]`)
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	sb.WriteString(`$`)
	return sb.String()
}

// Governing returns the documents whose refs cover at least one of the
// paths, in the order the documents were given.
func Governing(docs []store.DocRefs, paths []string) []store.DocRefs {
	var out []store.DocRefs
	for _, d := range docs {
		matchers := compile(d.Refs)
		if firstMatch(matchers, paths) != "" {
			out = append(out, d)
		}
	}
	return out
}

// RepoCoverage reports which directories of a code repository are governed
// by at least one document's refs.
type RepoCoverage struct {
	Repo string `json:"repo"`
	// Dirs is the number of directories examined (up to the configured
	// depth); Covered how many of them at least one ref reaches.
	Dirs    int `json:"dirs"`
	Covered int `json:"covered"`
	// Uncovered lists directories no document claims — the knowledge
	// base's blind spots for this repository.
	Uncovered []string `json:"uncovered"`
}

// Coverage matches directories against every document's refs. A directory
// counts as covered when some ref matches the directory itself or a file
// directly under it.
func Coverage(repo string, dirs []string, docs []store.DocRefs) RepoCoverage {
	var matchers []*regexp.Regexp
	for _, d := range docs {
		matchers = append(matchers, compile(d.Refs)...)
	}
	cov := RepoCoverage{Repo: repo, Dirs: len(dirs), Uncovered: []string{}}
	for _, dir := range dirs {
		if firstMatch(matchers, []string{dir, dir + "/file"}) != "" {
			cov.Covered++
		} else {
			cov.Uncovered = append(cov.Uncovered, dir)
		}
	}
	return cov
}
