package minilm

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// unigramTokenizer implements SentencePiece's Unigram model as exported in
// Hugging Face tokenizer.json files (XLM-R style): NFKC-like normalization,
// whitespace collapsing, Metaspace pre-tokenization ("▁" for spaces, one
// prepended), Viterbi segmentation over log-probability scores, and the
// <s> … </s> template.
type unigramTokenizer struct {
	scores   map[string]float64
	ids      map[string]int
	maxPiece int // longest piece in runes
	minScore float64
	unkID    int
	bosID    int
	eosID    int
	maxSeq   int
}

const metaspace = "▁"

// unkPenalty matches HF tokenizers: unknown characters score min_score - 10.
const unkPenalty = 10.0

func loadUnigram(path string, maxSeq int) (*unigramTokenizer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading tokenizer %s: %w", path, err)
	}
	var tj struct {
		Model struct {
			Type  string               `json:"type"`
			UnkID int                  `json:"unk_id"`
			Vocab [][2]json.RawMessage `json:"vocab"`
		} `json:"model"`
		AddedTokens []struct {
			ID      int    `json:"id"`
			Content string `json:"content"`
		} `json:"added_tokens"`
	}
	if err := json.Unmarshal(raw, &tj); err != nil {
		return nil, fmt.Errorf("parsing tokenizer %s: %w", path, err)
	}
	if tj.Model.Type != "Unigram" {
		return nil, fmt.Errorf("tokenizer %s: model type %q, want Unigram", path, tj.Model.Type)
	}
	t := &unigramTokenizer{scores: make(map[string]float64, len(tj.Model.Vocab)), ids: make(map[string]int, len(tj.Model.Vocab)),
		minScore: 0, unkID: tj.Model.UnkID, bosID: -1, eosID: -1, maxSeq: maxSeq}
	for i, entry := range tj.Model.Vocab {
		var piece string
		var score float64
		if err := json.Unmarshal(entry[0], &piece); err != nil {
			return nil, fmt.Errorf("tokenizer %s: vocab entry %d: %w", path, i, err)
		}
		if err := json.Unmarshal(entry[1], &score); err != nil {
			return nil, fmt.Errorf("tokenizer %s: vocab entry %d: %w", path, i, err)
		}
		t.scores[piece] = score
		t.ids[piece] = i
		if n := len([]rune(piece)); n > t.maxPiece {
			t.maxPiece = n
		}
		if score < t.minScore {
			t.minScore = score
		}
	}
	for _, a := range tj.AddedTokens {
		switch a.Content {
		case "<s>":
			t.bosID = a.ID
		case "</s>":
			t.eosID = a.ID
		}
	}
	if t.bosID < 0 || t.eosID < 0 {
		return nil, fmt.Errorf("tokenizer %s: missing <s>/</s> special tokens", path)
	}
	return t, nil
}

// encode returns <s> pieces… </s>, truncated to maxSeq.
func (t *unigramTokenizer) encode(text string) []int {
	ids := []int{t.bosID}
	s := normalizeForUnigram(text)
	if s != "" {
		ids = append(ids, t.viterbi(s)...)
	}
	if len(ids) > t.maxSeq-1 {
		ids = ids[:t.maxSeq-1]
	}
	return append(ids, t.eosID)
}

// normalizeForUnigram approximates SentencePiece's nmt_nfkc normalizer:
// NFKC, all whitespace to a single space, collapsed and trimmed, then
// Metaspace with a prepended "▁".
func normalizeForUnigram(text string) string {
	s := norm.NFKC.String(text)
	var sb strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if space && sb.Len() > 0 {
			sb.WriteString(metaspace)
		}
		space = false
		sb.WriteRune(r)
	}
	if sb.Len() == 0 {
		return ""
	}
	return metaspace + sb.String()
}

// viterbi finds the segmentation maximizing the sum of piece scores.
func (t *unigramTokenizer) viterbi(s string) []int {
	runes := []rune(s)
	n := len(runes)
	// byte offsets for substring extraction without re-encoding
	offs := make([]int, n+1)
	for i, r := range runes {
		offs[i+1] = offs[i] + len(string(r))
	}
	best := make([]float64, n+1)
	prev := make([]int, n+1)
	isUnk := make([]bool, n+1)
	for i := 1; i <= n; i++ {
		best[i] = math.Inf(-1)
	}
	unkScore := t.minScore - unkPenalty
	for i := 0; i < n; i++ {
		if math.IsInf(best[i], -1) {
			continue
		}
		matched := false
		for l := 1; l <= t.maxPiece && i+l <= n; l++ {
			piece := s[offs[i]:offs[i+l]]
			score, ok := t.scores[piece]
			if !ok {
				continue
			}
			matched = true
			if c := best[i] + score; c > best[i+l] {
				best[i+l], prev[i+l], isUnk[i+l] = c, i, false
			}
		}
		if !matched {
			if c := best[i] + unkScore; c > best[i+1] {
				best[i+1], prev[i+1], isUnk[i+1] = c, i, true
			}
		}
	}
	// walk back
	var rev []int
	var revUnk []bool
	for i := n; i > 0; i = prev[i] {
		if isUnk[i] {
			rev = append(rev, t.unkID)
			revUnk = append(revUnk, true)
		} else {
			rev = append(rev, t.ids[s[offs[prev[i]]:offs[i]]])
			revUnk = append(revUnk, false)
		}
	}
	// reverse and merge consecutive unknowns into one <unk>, as HF does
	out := make([]int, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		if revUnk[i] && len(out) > 0 && out[len(out)-1] == t.unkID && (i+1 < len(rev) && revUnk[i+1]) {
			continue
		}
		out = append(out, rev[i])
	}
	return out
}
