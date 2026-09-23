package metrics

import (
	"sort"
	"sync"
	"time"
)

// servedWindow bounds how long a document stays in the "recently served"
// set; servedMax bounds its size.
const (
	servedWindow = 24 * time.Hour
	servedMax    = 2000
)

// Served is a document that retrieval returned recently.
type Served struct {
	Path  string    `json:"path"`
	Count int       `json:"count"`
	Last  time.Time `json:"last"`
	// Via is the tool or route that served it last (compile_bundle, …).
	Via string `json:"via"`
}

// servedSet tracks which documents were handed to agents, in memory only:
// it restarts empty with the process and costs one map entry per document.
type servedSet struct {
	mu   sync.Mutex
	docs map[string]*Served
}

// RecordServed notes that name (a tool or route) returned these documents.
// Duplicate paths in one call count once.
func (r *Registry) RecordServed(name string, paths []string) {
	if r == nil || len(paths) == 0 {
		return
	}
	now := time.Now()
	s := &r.served
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.docs == nil {
		s.docs = map[string]*Served{}
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		d := s.docs[p]
		if d == nil {
			d = &Served{Path: p}
			s.docs[p] = d
		}
		d.Count++
		d.Last, d.Via = now, name
	}
	if len(s.docs) > servedMax {
		s.pruneLocked(now)
	}
}

func (s *servedSet) pruneLocked(now time.Time) {
	for p, d := range s.docs {
		if now.Sub(d.Last) > servedWindow {
			delete(s.docs, p)
		}
	}
	if len(s.docs) <= servedMax {
		return
	}
	all := make([]*Served, 0, len(s.docs))
	for _, d := range s.docs {
		all = append(all, d)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Last.After(all[j].Last) })
	for _, d := range all[servedMax:] {
		delete(s.docs, d.Path)
	}
}

// RecentlyServed returns documents served in the last 24 hours, most
// recent first.
func (r *Registry) RecentlyServed() []Served {
	if r == nil {
		return []Served{}
	}
	now := time.Now()
	s := &r.served
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Served, 0, len(s.docs))
	for _, d := range s.docs {
		if now.Sub(d.Last) <= servedWindow {
			out = append(out, *d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Path < out[j].Path
	})
	return out
}
