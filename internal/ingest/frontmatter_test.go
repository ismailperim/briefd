package ingest

import (
	"reflect"
	"testing"
)

func TestSplitFrontMatter(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantFM   FrontMatter
		wantRaw  string
		wantBody string
		wantErr  bool
	}{
		{
			name:     "no front matter",
			in:       "# Title\n\nBody",
			wantBody: "# Title\n\nBody",
		},
		{
			name:     "full front matter",
			in:       "---\ntitle: Retry policy\ntags: [payments, resilience]\nrefs: [\"services/payment/**\"]\nowner: platform\n---\n# Heading\n",
			wantFM:   FrontMatter{Title: "Retry policy", Tags: []string{"payments", "resilience"}, Refs: []string{"services/payment/**"}, Extra: map[string]any{"owner": "platform"}},
			wantRaw:  "title: Retry policy\ntags: [payments, resilience]\nrefs: [\"services/payment/**\"]\nowner: platform",
			wantBody: "# Heading\n",
		},
		{
			name:     "crlf line endings",
			in:       "---\r\ntitle: X\r\n---\r\nbody",
			wantFM:   FrontMatter{Title: "X"},
			wantRaw:  "title: X",
			wantBody: "body",
		},
		{
			name:     "horizontal rule is not front matter",
			in:       "--- not a delimiter\ntext",
			wantBody: "--- not a delimiter\ntext",
		},
		{
			name:    "unterminated",
			in:      "---\ntitle: X\nbody",
			wantErr: true,
		},
		{
			name:    "invalid yaml",
			in:      "---\ntitle: [\n---\nbody",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, raw, body, err := SplitFrontMatter([]byte(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(fm, tt.wantFM) {
				t.Errorf("fm = %+v, want %+v", fm, tt.wantFM)
			}
			if raw != tt.wantRaw {
				t.Errorf("raw = %q, want %q", raw, tt.wantRaw)
			}
			if string(body) != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}
