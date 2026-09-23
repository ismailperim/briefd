package ingest

import (
	"testing"
)

func TestExtractLinks(t *testing.T) {
	body := []byte("See [[Refund rules]] and [[Refund rules|the rules]] and [[glossary#Payout]].\n" +
		"Also [retries](../conventions/retries.md#backoff) and [ext](https://example.com/x.md) and [img](./a.png).\n" +
		"![[embedded]] ![pic](../x.md)\n" +
		"```\n[[not a link]] [no](../no.md)\n```\n`[[inline]]`\n")
	got := ExtractLinks(body)
	want := []Link{
		{Target: "Refund rules", Kind: "wikilink"},
		{Target: "glossary", Kind: "wikilink"},
		{Target: "../conventions/retries.md", Kind: "markdown"},
	}
	if len(got) != len(want) {
		t.Fatalf("links = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("link %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestResolver(t *testing.T) {
	r := NewResolver([]string{
		"domain/rules/refunds.md", "domain/glossary.md", "conventions/retries.md",
		"projects/a/notes.md", "projects/b/notes.md",
	})
	cases := []struct {
		from string
		link Link
		want string
	}{
		{"domain/rules/refunds.md", Link{"../glossary.md", "markdown"}, "domain/glossary.md"},
		{"domain/rules/refunds.md", Link{"../../conventions/retries.md", "markdown"}, "conventions/retries.md"},
		{"domain/rules/refunds.md", Link{"../missing.md", "markdown"}, ""},
		{"domain/glossary.md", Link{"refunds", "wikilink"}, "domain/rules/refunds.md"},
		{"domain/glossary.md", Link{"Refunds", "wikilink"}, "domain/rules/refunds.md"},
		{"domain/glossary.md", Link{"domain/rules/refunds", "wikilink"}, "domain/rules/refunds.md"},
		{"domain/glossary.md", Link{"rules/refunds.md", "wikilink"}, "domain/rules/refunds.md"},
		{"domain/glossary.md", Link{"notes", "wikilink"}, "projects/a/notes.md"}, // shortest, then alphabetical
		{"domain/glossary.md", Link{"nowhere", "wikilink"}, ""},
	}
	for _, c := range cases {
		if got := r.Resolve(c.from, c.link); got != c.want {
			t.Errorf("Resolve(%s, %+v) = %q, want %q", c.from, c.link, got, c.want)
		}
	}
}

func TestParseFrontMatterLinks(t *testing.T) {
	src := []byte("---\ntitle: A\nrelated:\n  - \"[[b]]\"\n  - \"[[c|C]]\"\n---\n# A\n\n## One\n\nsee [[d]]\n")
	doc, err := Parse("domain/a.md", src)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range doc.Links {
		got = append(got, l.Target)
	}
	if len(got) != 3 || got[0] != "b" || got[1] != "c" || got[2] != "d" {
		t.Errorf("links = %v, want [b c d]", got)
	}
}
