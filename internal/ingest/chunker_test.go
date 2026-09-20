package ingest

import (
	"strings"
	"testing"

	"github.com/ismailperim/briefd/internal/tokenizer"
)

func TestChunkDocument(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		body      string
		wantTitle string
		wantPaths []string
		wantHas   map[int]string // chunk index -> substring expected in content
	}{
		{
			name:      "title from H1, H2 and H3 sections",
			body:      "# Retry policy\n\nIntro paragraph.\n\n## Defaults\n\nFive attempts.\n\n### Backoff\n\nExponential with jitter.\n\n## Exceptions\n\nNone.\n",
			wantTitle: "Retry policy",
			wantPaths: []string{
				"Retry policy",
				"Retry policy > Defaults",
				"Retry policy > Defaults > Backoff",
				"Retry policy > Exceptions",
			},
			wantHas: map[int]string{0: "Intro paragraph.", 1: "## Defaults", 2: "### Backoff", 3: "None."},
		},
		{
			name:      "front matter title wins over H1; H1 line is dropped",
			title:     "Custom",
			body:      "# Ignored\n\n## A\n\ntext\n",
			wantTitle: "Custom",
			wantPaths: []string{"Custom > A"},
		},
		{
			name:      "no headings at all falls back to path",
			body:      "just text\n",
			wantTitle: "domain/x.md",
			wantPaths: []string{"domain/x.md"},
		},
		{
			name:      "H4 stays inside the H3 section",
			body:      "# T\n\n## S\n\n### Sub\n\ntext\n\n#### Deep\n\nmore\n",
			wantTitle: "T",
			wantPaths: []string{"T > S", "T > S > Sub"},
			wantHas:   map[int]string{1: "#### Deep"},
		},
		{
			name:      "H3 without enclosing H2",
			body:      "# T\n\n### Orphan\n\ntext\n",
			wantTitle: "T",
			wantPaths: []string{"T > Orphan"},
		},
		{
			name:      "headings inside fenced code are ignored",
			body:      "# T\n\n## Real\n\n```md\n## Not a heading\n```\n",
			wantTitle: "T",
			wantPaths: []string{"T > Real"},
			wantHas:   map[int]string{0: "## Not a heading"},
		},
		{
			name:      "duplicate breadcrumbs are disambiguated",
			body:      "# T\n\n## Same\n\none\n\n## Same\n\ntwo\n",
			wantTitle: "T",
			wantPaths: []string{"T > Same", "T > Same (2)"},
		},
		{
			name:      "setext headings",
			body:      "Title\n=====\n\nintro\n\nSection\n-------\n\nbody\n",
			wantTitle: "Title",
			wantPaths: []string{"Title", "Title > Section"},
			wantHas:   map[int]string{0: "intro", 1: "Section\n-------"},
		},
		{
			name:      "empty sections are skipped",
			body:      "# T\n\n## Empty\n\n## Full\n\ntext\n",
			wantTitle: "T",
			wantPaths: []string{"T > Empty", "T > Full"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, chunks := ChunkDocument("domain/x.md", tt.title, []byte(tt.body))
			if title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
			var paths []string
			for i, c := range chunks {
				paths = append(paths, c.HeadingPath)
				if c.Position != i {
					t.Errorf("chunk %d position = %d", i, c.Position)
				}
				if c.Tokens <= 0 || c.Tokens > MaxChunkTokens {
					t.Errorf("chunk %d tokens = %d", i, c.Tokens)
				}
				if len(c.ID) != 16 {
					t.Errorf("chunk %d id = %q", i, c.ID)
				}
			}
			if strings.Join(paths, "|") != strings.Join(tt.wantPaths, "|") {
				t.Errorf("paths = %v, want %v", paths, tt.wantPaths)
			}
			for i, want := range tt.wantHas {
				if i >= len(chunks) || !strings.Contains(chunks[i].Content, want) {
					t.Errorf("chunk %d does not contain %q", i, want)
				}
			}
		})
	}
}

func TestChunkIDsStable(t *testing.T) {
	body := "# T\n\n## A\n\nold text\n\n## B\n\nb\n"
	_, before := ChunkDocument("domain/x.md", "", []byte(body))
	_, after := ChunkDocument("domain/x.md", "", []byte(strings.Replace(body, "old text", "new text", 1)))
	if len(before) != len(after) {
		t.Fatalf("chunk count changed: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i].ID != after[i].ID {
			t.Errorf("chunk %d ID changed after content edit: %s -> %s", i, before[i].ID, after[i].ID)
		}
	}
	if before[0].ContentHash == after[0].ContentHash {
		t.Error("content hash did not change for edited chunk")
	}
	_, moved := ChunkDocument("domain/y.md", "", []byte(body))
	if moved[0].ID == before[0].ID {
		t.Error("chunk ID should depend on document path")
	}
}

func TestOversizedSectionIsSplit(t *testing.T) {
	para := strings.Repeat("Settlement batches are cut at midnight UTC and reconciled against acquirer reports. ", 8)
	var sb strings.Builder
	sb.WriteString("# T\n\n## Big\n\n")
	for range 12 {
		sb.WriteString(para)
		sb.WriteString("\n\n")
	}
	_, chunks := ChunkDocument("domain/x.md", "", []byte(sb.String()))
	if len(chunks) < 2 {
		t.Fatalf("expected the section to be split, got %d chunk(s)", len(chunks))
	}
	ids := map[string]bool{}
	for i, c := range chunks {
		if c.Tokens > MaxChunkTokens {
			t.Errorf("part %d has %d tokens > max %d", i, c.Tokens, MaxChunkTokens)
		}
		if c.HeadingPath != "T > Big" {
			t.Errorf("part %d heading path = %q", i, c.HeadingPath)
		}
		if ids[c.ID] {
			t.Errorf("duplicate chunk ID %s", c.ID)
		}
		ids[c.ID] = true
	}
	if chunks[0].Position != 0 || chunks[len(chunks)-1].Position != len(chunks)-1 {
		t.Error("positions are not sequential")
	}
}

func TestGiantParagraphNeverExceedsMax(t *testing.T) {
	word := strings.Repeat("x", 30) + " "
	giant := strings.Repeat(word, 400) // one paragraph, no newlines
	for _, part := range splitOversized(giant) {
		if n := tokenizer.Count(part); n > MaxChunkTokens {
			t.Errorf("part has %d tokens > %d", n, MaxChunkTokens)
		}
	}
}
