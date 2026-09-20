package ingest

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// FrontMatter holds the optional YAML header of a knowledge document
// (SPEC §2). Unknown keys are preserved in Extra so they survive a round trip.
type FrontMatter struct {
	Title string         `yaml:"title"`
	Tags  []string       `yaml:"tags"`
	Refs  []string       `yaml:"refs"`
	Extra map[string]any `yaml:",inline"`
}

var fmDelimiter = []byte("---")

// SplitFrontMatter separates a leading YAML front matter block from the
// Markdown body. Documents without front matter are returned unchanged with
// an empty FrontMatter and raw == "".
func SplitFrontMatter(src []byte) (fm FrontMatter, raw string, body []byte, err error) {
	if !bytes.HasPrefix(src, fmDelimiter) {
		return fm, "", src, nil
	}
	// The opening delimiter must be alone on the first line.
	firstNL := bytes.IndexByte(src, '\n')
	if firstNL < 0 || !bytes.Equal(bytes.TrimRight(src[:firstNL], "\r "), fmDelimiter) {
		return fm, "", src, nil
	}
	rest := src[firstNL+1:]
	end := -1
	offset := 0
	for offset <= len(rest) {
		nl := bytes.IndexByte(rest[offset:], '\n')
		var line []byte
		if nl < 0 {
			line = rest[offset:]
		} else {
			line = rest[offset : offset+nl]
		}
		if bytes.Equal(bytes.TrimRight(line, "\r "), fmDelimiter) {
			end = offset
			break
		}
		if nl < 0 {
			break
		}
		offset += nl + 1
	}
	if end < 0 {
		return fm, "", src, fmt.Errorf("front matter: missing closing delimiter")
	}
	yamlPart := rest[:end]
	if err := yaml.Unmarshal(yamlPart, &fm); err != nil {
		return fm, "", src, fmt.Errorf("front matter: %w", err)
	}
	afterEnd := end + len(fmDelimiter)
	if afterEnd < len(rest) && rest[afterEnd] == '\r' {
		afterEnd++
	}
	if afterEnd < len(rest) && rest[afterEnd] == '\n' {
		afterEnd++
	}
	return fm, string(bytes.TrimSpace(yamlPart)), rest[afterEnd:], nil
}
