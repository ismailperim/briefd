// Package sample embeds the fictional payments-platform knowledge base used
// by the tests and benchmarks, so `briefd demo` can stand up a working
// instance without a checkout. The canonical copy is testdata/knowledge;
// `make sample-sync` mirrors it here and CI fails when the two differ.
package sample

import (
	"embed"

	"github.com/ismailperim/briefd/internal/scaffold"
)

//go:embed all:knowledge
var corpusFS embed.FS

// Write copies the corpus into dir, keeping any files that already exist.
func Write(dir string) ([]string, error) {
	return scaffold.WriteFS(corpusFS, "knowledge", dir)
}
