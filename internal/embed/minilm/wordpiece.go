package minilm

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// tokenizer implements BERT's uncased tokenization: basic cleanup and
// punctuation/CJK splitting, lower-casing with accent stripping, then
// greedy longest-match-first WordPiece.
type tokenizer struct {
	vocab        map[string]int
	maxWordChars int
	maxSeq       int // including [CLS] and [SEP]
	unkID, clsID int
	sepID        int
}

func loadVocab(path string) (map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading vocab %s: %w", path, err)
	}
	defer f.Close()
	vocab := make(map[string]int, 31000)
	sc := bufio.NewScanner(f)
	for i := 0; sc.Scan(); i++ {
		vocab[strings.TrimRight(sc.Text(), "\r")] = i
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading vocab %s: %w", path, err)
	}
	return vocab, nil
}

func newTokenizer(vocab map[string]int, maxSeq int) (*tokenizer, error) {
	t := &tokenizer{vocab: vocab, maxWordChars: 100, maxSeq: maxSeq}
	var ok [3]bool
	t.unkID, ok[0] = vocab["[UNK]"]
	t.clsID, ok[1] = vocab["[CLS]"]
	t.sepID, ok[2] = vocab["[SEP]"]
	if !ok[0] || !ok[1] || !ok[2] {
		return nil, fmt.Errorf("vocab is missing [UNK]/[CLS]/[SEP]")
	}
	return t, nil
}

// encode returns token ids for text, wrapped in [CLS] ... [SEP] and
// truncated to maxSeq.
func (t *tokenizer) encode(text string) []int {
	ids := make([]int, 0, 64)
	ids = append(ids, t.clsID)
	for _, word := range basicTokenize(text) {
		ids = t.wordpiece(word, ids)
		if len(ids) >= t.maxSeq-1 {
			ids = ids[:t.maxSeq-1]
			break
		}
	}
	return append(ids, t.sepID)
}

func (t *tokenizer) wordpiece(word string, ids []int) []int {
	runes := []rune(word)
	if len(runes) > t.maxWordChars {
		return append(ids, t.unkID)
	}
	start := 0
	var pieces []int
	for start < len(runes) {
		end := len(runes)
		found := -1
		for start < end {
			sub := string(runes[start:end])
			if start > 0 {
				sub = "##" + sub
			}
			if id, ok := t.vocab[sub]; ok {
				found = id
				break
			}
			end--
		}
		if found < 0 {
			return append(ids, t.unkID)
		}
		pieces = append(pieces, found)
		start = end
	}
	return append(ids, pieces...)
}

// basicTokenize lower-cases, strips accents (NFD + drop Mn), and splits on
// whitespace, punctuation and CJK characters, mirroring BertTokenizer's
// BasicTokenizer with do_lower_case=True.
func basicTokenize(text string) []string {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range norm.NFD.String(strings.ToLower(text)) {
		switch {
		case r == 0 || r == 0xFFFD || unicode.Is(unicode.Cc, r) && !unicode.IsSpace(r):
			continue // control characters are dropped
		case unicode.Is(unicode.Mn, r):
			continue // combining marks: accent stripping
		case unicode.IsSpace(r):
			flush()
		case isPunct(r) || isCJK(r):
			flush()
			tokens = append(tokens, string(r))
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// isPunct matches BERT's _is_punctuation: ASCII non-alphanumeric printable
// characters plus Unicode P* categories.
func isPunct(r rune) bool {
	if (r >= 33 && r <= 47) || (r >= 58 && r <= 64) || (r >= 91 && r <= 96) || (r >= 123 && r <= 126) {
		return true
	}
	return unicode.IsPunct(r)
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) || (r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0x2A700 && r <= 0x2B73F) || (r >= 0x2B740 && r <= 0x2B81F) || (r >= 0x2B820 && r <= 0x2CEAF) ||
		(r >= 0xF900 && r <= 0xFAFF) || (r >= 0x2F800 && r <= 0x2FA1F)
}
