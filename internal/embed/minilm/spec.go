package minilm

import "fmt"

// TokenizerKind selects the tokenizer implementation.
type TokenizerKind string

// Tokenizer kinds.
const (
	WordPiece TokenizerKind = "wordpiece" // BERT uncased vocab.txt
	Unigram   TokenizerKind = "unigram"   // SentencePiece via tokenizer.json (XLM-R)
)

// File is a downloadable model file with its pinned digest.
type File struct {
	Name   string
	SHA256 string
}

// Spec describes a supported BERT-family sentence encoder.
type Spec struct {
	Name         string
	Dim          int
	Layers       int
	Heads        int
	Intermediate int
	VocabSize    int
	MaxPositions int
	// MaxSeq is the sequence length used for encoding (≤ MaxPositions).
	MaxSeq       int
	LayerNormEps float64
	Tokenizer    TokenizerKind
	// QueryPrefix / PassagePrefix are prepended to queries and documents
	// (E5-style models are trained with them).
	QueryPrefix   string
	PassagePrefix string
	Files         []File
	BaseURL       string
	// Languages is a human-readable note for docs and `briefd model list`.
	Languages string
}

// Specs lists the models briefd can run in-process.
var Specs = map[string]Spec{
	"all-MiniLM-L6-v2": {
		Name: "all-MiniLM-L6-v2", Dim: 384, Layers: 6, Heads: 12, Intermediate: 1536, VocabSize: 30522,
		MaxPositions: 512, MaxSeq: 256, LayerNormEps: 1e-12, Tokenizer: WordPiece,
		Files: []File{
			{"model.safetensors", "53aa51172d142c89d9012cce15ae4d6cc0ca6895895114379cacb4fab128d9db"},
			{"vocab.txt", "07eced375cec144d27c900241f3e339478dec958f92fddbc551f295c992038a3"},
		},
		BaseURL:   "https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/",
		Languages: "English only (fast: 6 layers, 87 MB)",
	},
	"multilingual-e5-small": {
		Name: "multilingual-e5-small", Dim: 384, Layers: 12, Heads: 12, Intermediate: 1536, VocabSize: 250037,
		MaxPositions: 512, MaxSeq: 512, LayerNormEps: 1e-12, Tokenizer: Unigram,
		QueryPrefix: "query: ", PassagePrefix: "passage: ",
		Files: []File{
			{"model.safetensors", "1a55775f53449dac10a2bcbc312469fac40b96d53198c407081a831f81c98477"},
			{"tokenizer.json", "0b44a9d7b51c3c62626640cda0e2c2f70fdacdc25bbbd68038369d14ebdf4c39"},
		},
		BaseURL:   "https://huggingface.co/intfloat/multilingual-e5-small/resolve/main/",
		Languages: "100+ languages incl. Turkish (12 layers, 470 MB)",
	},
}

// DefaultModel is used when the configuration names none. The multilingual
// model wins on the golden sets overall and is far better outside English
// (ADR-0006); all-MiniLM-L6-v2 remains available for English-only speed.
const DefaultModel = "multilingual-e5-small"

// SpecFor returns the spec for name.
func SpecFor(name string) (Spec, error) {
	if name == "" {
		name = DefaultModel
	}
	s, ok := Specs[name]
	if !ok {
		return Spec{}, fmt.Errorf("unknown local embedding model %q (available: all-MiniLM-L6-v2, multilingual-e5-small)", name)
	}
	return s, nil
}
