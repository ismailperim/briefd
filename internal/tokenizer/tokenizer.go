// Package tokenizer estimates token counts without depending on a specific
// model's BPE vocabulary.
//
// The estimate is calibrated against OpenAI's cl100k/o200k family, which the
// Claude family tracks closely for English prose and code. Typical error is
// within ±15% on Markdown; callers that enforce a budget must apply their own
// headroom (SPEC §5 mandates 5%). The heuristic is deliberately simple so it
// is fast, deterministic and dependency-free; it can be swapped for a real BPE
// tokenizer behind the same function if the eval shows the margin matters.
package tokenizer

import (
	"unicode"
	"unicode/utf8"
)

// Count returns the estimated number of tokens in s.
func Count(s string) int {
	if s == "" {
		return 0
	}

	tokens := 0
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\n':
			// Newlines usually become their own token; runs of blank lines
			// collapse into one.
			tokens++
			i += size
			for i < len(s) && (s[i] == '\n' || s[i] == '\r') {
				i++
			}
		case unicode.IsSpace(r):
			// Spaces attach to the following token.
			i += size
		case unicode.IsLetter(r):
			start, ascii := i, true
			for i < len(s) {
				r, size = utf8.DecodeRuneInString(s[i:])
				if !unicode.IsLetter(r) && r != '\'' {
					break
				}
				if r >= utf8.RuneSelf {
					ascii = false
				}
				i += size
			}
			tokens += wordTokens(s[start:i], ascii)
		case unicode.IsDigit(r):
			// BPE vocabularies group up to three digits per token.
			start := i
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
			tokens += ceilDiv(i-start, 3)
		default:
			// Punctuation and symbols: usually one token each, with common
			// pairs ("//", "->", "==", ")." ...) merged. Count runs of the
			// same class at roughly two chars per token.
			start := i
			for i < len(s) {
				r, size = utf8.DecodeRuneInString(s[i:])
				if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
					break
				}
				i += size
			}
			tokens += ceilDiv(utf8.RuneCountInString(s[start:i]), 2)
		}
	}
	return tokens
}

// wordTokens estimates tokens for a single word. ASCII words average ~4.5
// characters per token; non-Latin/accented words tokenize into shorter
// pieces because they are rarer in BPE vocabularies.
func wordTokens(word string, ascii bool) int {
	n := utf8.RuneCountInString(word)
	if ascii {
		if n <= 6 {
			return 1
		}
		return ceilDiv(n, 5)
	}
	return ceilDiv(n, 3)
}

func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}
