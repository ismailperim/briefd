// Package scaffold creates a starter knowledge repository (SPEC §2 layout)
// from embedded templates.
package scaffold

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:template
var templateFS embed.FS

// Write creates the starter layout under dir. Existing files are never
// overwritten; the returned slice lists the files that were created.
func Write(dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	var created []string
	err := fs.WalkDir(templateFS, "template", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("template", p)
		if rel == "." {
			return nil
		}
		dst := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o750)
		}
		if _, err := os.Stat(dst); err == nil {
			return nil // keep the user's file
		}
		data, err := templateFS.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil { //nolint:gosec // documentation files are meant to be world-readable
			return fmt.Errorf("writing %s: %w", dst, err)
		}
		created = append(created, rel)
		return nil
	})
	return created, err
}
